// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

// doctorServer is an instance that answers the requests of gt doctor for alice/project.
func doctorServer(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"28.1.0"}`))
		case "/api/v1/token":
			_, _ = w.Write([]byte(`{"scopes":["all"]}`))
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// doctorGit is a Git checkout of alice/project at url, without gt as credential helper.
func doctorGit(url string) *fakeGit {
	return &fakeGit{results: map[string]string{"--version": "git version 2.50.1", "remote": "origin", "remote get-url origin": url + "/alice/project.git"}}
}

func TestEnvironmentCredentialReportsActualAccount(t *testing.T) {
	server := newAuthenticatedServer(doctorServer(t))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{"--version": "git version 2.50.1"}}
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_USER": "ALICE"})
	require.NoError(t, Execute(context.Background(), deps, []string{"doctor", "--json"}))
	require.Contains(t, out.String(), `"user":"alice"`)
	require.Contains(t, out.String(), `"user_source":"GITEA_CLI_USER"`)
}

func TestDoctorDiscoversRepositoryBestEffort(t *testing.T) {
	server := newAuthenticatedServer(doctorServer(t))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = doctorGit(server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"doctor", "--json=repo,repo_source"}))
	require.JSONEq(t, `{"repo":"alice/project","repo_source":"remote origin"}`, out.String())
}

func TestDoctorReportsEveryCheckInsteadOfFailingFirst(t *testing.T) {
	server := newAuthenticatedServer(doctorServer(t))
	defer server.Close()
	origin := `(git remote "origin")`
	for _, tc := range []struct {
		name     string
		env      map[string]string
		args     []string
		statuses string
		account  string
	}{
		{
			name: "environment token", env: map[string]string{"GITEA_CLI_HOST": server.URL, "GITEA_TOKEN": "token"},
			statuses: "Config ok, Instance ok, Account ok, Repository ok, Git warning",
			account:  "alice (GITEA_TOKEN authenticated account) · credential valid · scopes: all",
		},
		{
			name: "no account for the instance of the remote", env: map[string]string{"GITEA_CLI_HOST": server.URL},
			statuses: "Config ok, Instance ok, Account failed, Repository skipped, Git warning",
			account:  "no account for " + server.URL + " (instance from " + strings.Trim(origin, "()") + "); run gt auth login --host " + server.URL + ", or use -R to target another repository",
		},
		{
			name: "token of another instance", env: map[string]string{"GITEA_CLI_HOST": "https://other.example.org", "GITEA_TOKEN": "token"},
			statuses: "Config ok, Instance ok, Account failed, Repository skipped, Git warning",
			account:  "GITEA_TOKEN belongs to https://other.example.org, but the instance here is " + server.URL + " " + origin,
		},
		{
			name: "instance that does not answer", args: []string{"--host", "http://127.0.0.1:1", "-R", "alice/project"},
			statuses: "Config ok, Instance failed, Account skipped, Repository skipped, Git warning",
			account:  "not checked while the instance does not answer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := testDeps(t)
			deps.Git = doctorGit(server.URL)
			deps.Getenv = envFunc(tc.env)
			err := Execute(context.Background(), deps, append([]string{"doctor", "--json=checks"}, tc.args...))
			var report doctorReport
			require.NoError(t, json.Unmarshal(out.Bytes(), &report), out.String())
			statuses := make([]string, len(report.Checks))
			for i, check := range report.Checks {
				statuses[i] = check.Name + " " + check.Status
				if check.Name == "Account" {
					require.Equal(t, tc.account, check.Detail)
				}
			}
			require.Equal(t, tc.statuses, strings.Join(statuses, ", "))
			if strings.Contains(tc.statuses, "failed") {
				require.Equal(t, clierr.Failure, clierr.ExitCode(err))
				require.EqualError(t, err, "1 of 5 checks failed")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDoctorShowsChecksOnATerminal(t *testing.T) {
	server := newAuthenticatedServer(doctorServer(t))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Streams.OutTTY = true
	deps.Git = doctorGit(server.URL)
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice"), SSHHost: "ssh.example.org:2222"})
	require.NoError(t, deps.Credentials.Save(server.URL, "alice", settings.StoreFile, auth.Credential{AccessToken: "alice-token"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"doctor"}))
	lines := withoutANSI(out.String())
	for _, want := range []string{
		"✓  Config      config.yml and hosts.yml in " + deps.Settings.Dir + "\n",
		"✓  Account     alice (active instance account) · credential valid · scopes: all\n",
		"✓  Repository  alice/project (git remote \"origin\") · default branch main\n",
		"!  Git         git version 2.50.1 · credential helper not configured for " + server.URL + "\n" +
			"               → gt auth setup-git, if pushes over HTTPS should use this account\n",
		"✓  SSH         remotes connect to ssh.example.org:2222 (ssh_host)\n",
	} {
		require.Contains(t, lines, want)
	}
	require.Regexp(t, `✓  Instance    `+server.URL+` \(git remote "origin"\) · Gitea 28.1.0 · \d+ms\n`, lines)
}
