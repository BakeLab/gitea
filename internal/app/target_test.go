// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/settings"
	"gitea.dev/tea/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestResolvePromptsForAccountWhenNoneActive(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/user/repos", r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	prompt := &scriptedPrompter{input: "Bob"}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	configureAccounts(t, deps, server.URL, "", "alice", "bob")
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL})
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list", "--json", "--interactive"}))
	require.Len(t, prompt.asked, 1)
	require.JSONEq(t, `[]`, out.String())
}

func TestResolveUsesSoleConfiguredAccount(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	configureAccounts(t, deps, server.URL, "", "alice")
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL})
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "list", "--json", "--no-input"}))
	require.Empty(t, prompt.asked)
	require.JSONEq(t, `[]`, out.String())
}

func TestAccountNamesMatchCaseInsensitively(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token alice-token", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	for name, test := range map[string]struct {
		args    []string
		user    string
		wantErr string
	}{
		"--user":       {args: []string{"repo", "list", "--json", "--user", "ALICE"}},
		"TEA_USER":     {args: []string{"repo", "list", "--json"}, user: "ALICE"},
		"config get":   {args: []string{"config", "get", "ssh_key", "--host", server.URL, "--user", "ALICE"}},
		"config set":   {args: []string{"config", "set", "ssh_key", "/key", "--host", server.URL, "--user", "ALICE"}},
		"auth logout":  {args: []string{"auth", "logout", "--user", "ALICE", "--yes"}},
		"auth refresh": {args: []string{"auth", "refresh", "--user", "ALICE"}, wantErr: "only OAuth accounts can be refreshed"},
	} {
		t.Run(name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			configureAccounts(t, deps, server.URL, "", "alice", "bob")
			deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL, "TEA_USER": test.user})
			err := Execute(context.Background(), deps, test.args)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEnvironmentCredentialVerifiesSelectedAccountBeforeMutation(t *testing.T) {
	for _, selection := range []string{"--user", "TEA_USER", "active instance account"} {
		t.Run(selection, func(t *testing.T) {
			reads, mutations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/base/api/v1/user" {
					reads++
					require.Equal(t, "token private-test-token", r.Header.Get("Authorization"))
					_, _ = w.Write([]byte(`{"login":"alice"}`))
					return
				}
				mutations++
				t.Error("operation ran as the wrong account")
			}))
			defer server.Close()
			deps, out, diagnostics := testDeps(t)
			host := server.URL + "/base"
			env := map[string]string{"TEA_HOST": host, "GITEA_TOKEN": "private-test-token"}
			if selection == "TEA_USER" {
				env["TEA_USER"] = "bob"
			}
			deps.Getenv = envFunc(env)
			args := []string{"issue", "create", "-R", "alice/project", "--title", "Issue", "--body", "Body", "--no-input", "--json"}
			if selection == "--user" {
				args = append(args, "--user", "bob")
			}
			if selection == "active instance account" {
				configureHost(t, deps, host, settings.Host{User: "bob", Users: tokenAccounts(settings.StoreKeyring, "bob")})
			}
			err := Execute(context.Background(), deps, args)
			require.ErrorContains(t, err, "does not belong to the selected account")
			require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
			require.Equal(t, 1, reads)
			require.Zero(t, mutations)
			require.Empty(t, out.String())
			require.Empty(t, diagnostics.String())
			require.NotContains(t, err.Error(), "private-test-token")
			files, err := os.ReadDir(deps.Settings.Dir)
			require.NoError(t, err)
			for _, file := range files {
				require.NotContains(t, file.Name(), "credentials")
			}
		})
	}
}

func TestIssueCreateUsesEnvironmentCredentialAndCleanJSON(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/base/api/v1/repos/alice/project/issues", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "token sensitive", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "Title", body["title"])
		require.Equal(t, "Body", body["body"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"number":3,"title":"Title","body":"Body","html_url":"https://example.org/alice/project/issues/3","state":"open"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": server.URL + "/base", "GITEA_TOKEN": "sensitive"})
	err := Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--title", "Title", "--body", "Body", "--json=number,url"})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.JSONEq(t, `{"number":3,"url":"https://example.org/alice/project/issues/3"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestEnvironmentTokenCannotCrossInstances(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_INSTANCE_URL": "https://a.example.org", "GITEA_TOKEN": "secret"})
	err := Execute(context.Background(), deps, []string{"repo", "list", "--host", "https://b.example.org"})
	require.Error(t, err)
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	require.NotContains(t, err.Error(), "secret")
}

func TestEnvironmentLoginHonorsInstanceInsecureAndSSHHost(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		require.Equal(t, "/api/v1/repos/alice/project", r.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	environment := map[string]string{"GITEA_INSTANCE_URL": server.URL, "GITEA_TOKEN": "secret"}
	deps, _, _ := testDeps(t)
	deps.Getenv = envFunc(environment)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": "git@ssh.example.org:alice/project.git"}}
	explicit := []string{"api", "repos/{owner}/{repo}", "-R", "alice/project"}
	require.ErrorContains(t, Execute(context.Background(), deps, explicit), "certificate")
	environment["GITEA_INSTANCE_INSECURE"] = "true"
	require.NoError(t, Execute(context.Background(), deps, explicit))
	discovered := []string{"api", "repos/{owner}/{repo}"}
	require.ErrorContains(t, Execute(context.Background(), deps, discovered), "SSH remote does not match")
	environment["GITEA_INSTANCE_SSH_HOST"] = "ssh.example.org"
	require.NoError(t, Execute(context.Background(), deps, discovered))
	environment["GITEA_INSTANCE_INSECURE"] = "maybe"
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, explicit)))
	_, err := os.Stat(filepath.Join(deps.Settings.Dir, "hosts.yml"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRepositoryResolutionPrecedence(t *testing.T) {
	deps, _, _ := testDeps(t)
	server := newAuthenticatedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected workflow request") }))
	defer server.Close()
	deps.Client = func(_, token string, insecure bool) *transport.Client {
		return transport.New(server.URL, token, insecure)
	}
	deps.Getenv = envFunc(map[string]string{"TEA_HOST": "https://example.org", "GITEA_TOKEN": "secret"})
	runner := &fakeGit{results: map[string]string{
		"symbolic-ref --short HEAD":       "main",
		"config --get branch.main.remote": "feature-remote",
		"remote get-url feature-remote":   "https://example.org/owner/current.git",
		"remote get-url upstream":         "https://example.org/owner/upstream.git",
		"config --get tea.repository":     "https://example.org/owner/saved",
	}}
	deps.Git = runner
	application := application{deps: deps}
	resolved, err := application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/saved", resolved.Repo)
	require.Equal(t, []string{"config --get tea.repository"}, runner.calls)
	application.options.repo = "owner/explicit"
	runner.calls = nil
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/explicit", resolved.Repo)
	require.Empty(t, runner.calls)
	application.options.repo = ""
	delete(runner.results, "config --get tea.repository")
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/current", resolved.Repo)
	application.options.remote = "upstream"
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/upstream", resolved.Repo)
	require.False(t, runner.prompt)
}

func TestRepositoryDiscoveryOutsideCheckoutWritesNoGitDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "plain")
	require.NoError(t, os.Mkdir(directory, 0o700))
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	deps, out, diagnostics := serverDeps(t, "https://example.test")
	// Match production wiring, where the Git runner writes to the user's stderr.
	deps.Git = git.System{In: deps.Streams.In, ErrOut: diagnostics}
	err := Execute(context.Background(), deps, []string{"issue", "list", "--cwd", directory})
	require.ErrorContains(t, err, "repository required")
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}
