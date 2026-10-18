// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"
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
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL})
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
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL})
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
		"--user":         {args: []string{"repo", "list", "--json", "--user", "ALICE"}},
		"GITEA_CLI_USER": {args: []string{"repo", "list", "--json"}, user: "ALICE"},
		"config get":     {args: []string{"config", "get", "ssh_key", "--host", server.URL, "--user", "ALICE"}},
		"config set":     {args: []string{"config", "set", "ssh_key", "/key", "--host", server.URL, "--user", "ALICE"}},
		"auth logout":    {args: []string{"auth", "logout", "--user", "ALICE", "--yes"}},
		"auth refresh":   {args: []string{"auth", "refresh", "--user", "ALICE"}, wantErr: "only OAuth accounts can be refreshed"},
	} {
		t.Run(name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			configureAccounts(t, deps, server.URL, "", "alice", "bob")
			deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL, "GITEA_CLI_USER": test.user})
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
	for _, selection := range []string{"--user", "GITEA_CLI_USER"} {
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
			env := map[string]string{"GITEA_CLI_HOST": host, "GITEA_TOKEN": "private-test-token"}
			if selection == "GITEA_CLI_USER" {
				env["GITEA_CLI_USER"] = "bob"
			}
			deps.Getenv = envFunc(env)
			args := []string{"issue", "create", "-R", "alice/project", "--title", "Issue", "--body", "Body", "--no-input", "--json"}
			if selection == "--user" {
				args = append(args, "--user", "bob")
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
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL + "/base", "GITEA_TOKEN": "sensitive"})
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
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": "https://example.org", "GITEA_TOKEN": "secret"})
	runner := &fakeGit{results: map[string]string{
		"symbolic-ref --quiet HEAD":       "main",
		"config --get branch.main.remote": "feature-remote",
		"remote get-url feature-remote":   "https://example.org/owner/current.git",
		"remote get-url upstream":         "https://example.org/owner/upstream.git",
		"config --get gt.repository":      "https://example.org/owner/saved",
	}}
	deps.Git = runner
	application := application{deps: deps}
	resolved, err := application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/saved", resolved.Repo)
	require.Equal(t, []string{"config --get gt.repository"}, runner.calls)
	application.options.repo = "owner/explicit"
	runner.calls = nil
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/explicit", resolved.Repo)
	require.Empty(t, runner.calls)
	application.options.repo = ""
	delete(runner.results, "config --get gt.repository")
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/current", resolved.Repo)
	application.options.remote = "upstream"
	resolved, err = application.resolve(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "owner/upstream", resolved.Repo)
	require.False(t, runner.prompt)
}

func TestRepositoryDiscoveryFindsGiteaRemoteBesideAnotherForge(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"full_name":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/repos/") + `"}`))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name    string
		remotes map[string]string
		args    []string
		env     map[string]string
		want    string
	}{
		{"origin on GitHub", map[string]string{"origin": "https://github.com/example/project.git", "gitea": server.URL + "/alice/project.git"}, nil, nil, "alice/project"},
		{"no origin", map[string]string{"github": "https://github.com/example/project.git", "gitea": server.URL + "/alice/project.git"}, nil, nil, "alice/project"},
		{"--host selects its remote", map[string]string{"origin": "https://gitea.com/alice/project.git", "gitea": server.URL + "/bob/tool.git"}, []string{"--host", server.URL}, nil, "bob/tool"},
		{"GITEA_TOKEN selects its instance", map[string]string{"origin": "https://gitea.com/carol/project.git", "gitea": server.URL + "/alice/project.git"}, nil, map[string]string{"GITEA_TOKEN": "token", "GITEA_CLI_HOST": server.URL}, "alice/project"},
		{"remotes naming one repository", map[string]string{"origin": "https://github.com/example/project.git", "gitea": server.URL + "/alice/project.git", "gitea-ssh": "git@127.0.0.1:alice/project.git"}, nil, nil, "alice/project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			if tc.env != nil {
				deps.Getenv = envFunc(tc.env)
			}
			configureAccounts(t, deps, server.URL, "alice", "alice")
			configureAccounts(t, deps, "https://gitea.com", "carol", "carol")
			results := map[string]string{"remote": strings.Join(slices.Sorted(maps.Keys(tc.remotes)), "\n")}
			for name, url := range tc.remotes {
				results["remote get-url "+name] = url
			}
			deps.Git = &fakeGit{results: results}
			require.NoError(t, Execute(context.Background(), deps, append([]string{"repo", "view", "--no-input", "--json=full_name"}, tc.args...)))
			require.JSONEq(t, `{"full_name":"`+tc.want+`"}`, out.String())
		})
	}
}

func TestRepositoryArgumentsAcceptShorthands(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"full_name":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/repos/") + `"}`))
	}))
	defer server.Close()
	bare := strings.TrimPrefix(server.URL, "http://")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"bare name belongs to the active account", []string{"repo", "view", "project"}, "alice/project"},
		{"bare name belongs to the sudo user", []string{"repo", "view", "project", "--sudo", "bob"}, "bob/project"},
		{".git suffix", []string{"repo", "view", "bob/tool.git"}, "bob/tool"},
		{"HOST/OWNER/REPO", []string{"repo", "view", bare + "/bob/tool"}, "bob/tool"},
		{"--repo HOST/OWNER/REPO", []string{"repo", "view", "-R", bare + "/bob/tool.git"}, "bob/tool"},
		{"bare --host", []string{"repo", "view", "bob/tool", "--host", bare}, "bob/tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			configureAccounts(t, deps, server.URL, "alice", "alice", "bob")
			require.NoError(t, Execute(context.Background(), deps, append(tc.args, "--no-input", "--json=full_name")))
			require.JSONEq(t, `{"full_name":"`+tc.want+`"}`, out.String())
		})
	}
}

func TestRepositoryNamedLikeAGitRemoteSuggestsRemote(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv("https://example.org", nil)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin\nupstream"}}
	err := Execute(context.Background(), deps, []string{"issue", "list", "-R", "origin"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, `invalid repository "origin": use OWNER/REPO, HOST/OWNER/REPO or a URL; did you mean --remote origin?`)
	err = Execute(context.Background(), deps, []string{"issue", "list", "-R", "a/b/c/d"})
	require.EqualError(t, err, `invalid repository "a/b/c/d": use OWNER/REPO, HOST/OWNER/REPO or a URL`)
}

func TestAccountErrorsNameTheInstanceAndAccounts(t *testing.T) {
	const instance = "https://example.org"
	remote := map[string]string{"remote": "origin", "remote get-url origin": "https://gitea.com/alice/project.git"}
	for _, tc := range []struct {
		name   string
		users  []string
		active string
		args   []string
		git    map[string]string
		want   string
	}{
		{"unknown --user", []string{"alice", "bob"}, "alice", []string{"-R", "alice/project", "--user", "nobody"}, nil, `account "nobody" is not logged in on https://example.org (accounts: alice, bob)`},
		{"unknown --user without accounts", nil, "", []string{"-R", "alice/project", "--user", "nobody", "--host", instance}, nil, `account "nobody" is not logged in on https://example.org; run gt auth login --host https://example.org`},
		{"no active account", []string{"alice", "bob"}, "", []string{"-R", "alice/project"}, nil, "no active account on https://example.org (accounts: alice, bob); pass --user or run gt auth switch"},
		{"instance from a remote", []string{"alice"}, "alice", nil, remote, `no account for https://gitea.com (instance from git remote "origin"); run gt auth login --host https://gitea.com, or use -R to target another repository`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			if tc.users != nil {
				configureAccounts(t, deps, instance, tc.active, tc.users...)
			}
			if tc.git != nil {
				deps.Git = &fakeGit{results: tc.git}
			}
			err := Execute(context.Background(), deps, append([]string{"issue", "list", "--no-input"}, tc.args...))
			require.Equal(t, clierr.Authentication, clierr.ExitCode(err), "%v", err)
			require.EqualError(t, err, tc.want)
		})
	}
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

func TestEnvironmentCredentialOverridesTheActiveAccount(t *testing.T) {
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token private-test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/base/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		created = true
		_, _ = w.Write([]byte(`{"id":9,"number":3,"title":"Issue","state":"open"}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	host := server.URL + "/base"
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": host, "GITEA_TOKEN": "private-test-token"})
	configureHost(t, deps, host, settings.Host{User: "bob", Users: tokenAccounts(settings.StoreKeyring, "bob")})
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--title", "Issue", "--body", "Body", "--no-input", "--json"}))
	require.True(t, created)
}

func TestRepositoryFlagNamedLikeADirectoryIsAmbiguous(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"full_name":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/repos/") + `"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "project"), 0o755))
	deps, out, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice")
	err := Execute(context.Background(), deps, []string{"repo", "view", "-R", "project", "--cwd", dir, "--no-input", "--json=full_name"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `"project" is both a repository name and a directory here; use OWNER/project`)
	// A qualified name, or a bare name without such a directory, is unambiguous.
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "-R", "alice/project", "--cwd", dir, "--no-input", "--json=full_name"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "view", "-R", "other", "--cwd", dir, "--no-input", "--json=full_name"}))
	require.Contains(t, out.String(), "alice/other")
}
