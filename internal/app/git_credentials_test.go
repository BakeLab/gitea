// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
	"github.com/stretchr/testify/require"
)

func TestSetupGitRequiresAnAccountOfTheInstance(t *testing.T) {
	deps, _, _ := testDeps(t)
	manager := testManager(t, deps)
	instance := "https://example.test/gitea"
	require.NoError(t, manager.Register(instance, "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "alice-token"}))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--host", "http://nothere:1"}, "no account for http://nothere:1; run gt auth login --host http://nothere:1"},
		{[]string{"--host", instance, "--user", "nobody"}, `account "nobody" is not logged in on ` + instance + " (accounts: alice)"},
	} {
		git := &fakeGit{}
		deps.Git = git
		err := Execute(context.Background(), deps, append([]string{"auth", "setup-git", "--no-input"}, tc.args...))
		require.ErrorContains(t, err, tc.want)
		require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
		require.Empty(t, git.calls, "no helper is configured")
	}
}

func TestSetupGitConfiguresTheInstanceTLSSettings(t *testing.T) {
	gittest.Isolate(t)
	deps, _, _ := testDeps(t)
	deps.Git = git.System{}
	instance := "https://example.test/gitea"
	options := auth.HostOptions{Insecure: true, ClientCert: "/certs/client.pem", ClientKey: "/certs/client.key"}
	require.NoError(t, testManager(t, deps).Register(instance, "alice", "file", "token", options, auth.Credential{AccessToken: "alice-token"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "setup-git", "--host", instance, "--no-input"}))
	values, err := deps.Git.Output(context.Background(), "", false, "config", "--global", "--get-regexp", `^http\.`)
	require.NoError(t, err)
	prefix := "http." + instance + "."
	require.Equal(t, prefix+"sslcert /certs/client.pem\n"+prefix+"sslkey /certs/client.key\n"+prefix+"sslverify false", values)
}

func TestGitCredentialHelperSelectsAccountsAndDoesNotMutateFromGit(t *testing.T) {
	deps, out, diagnostics := testDeps(t)
	manager := testManager(t, deps)
	instance := "https://example.test/gitea"
	require.NoError(t, manager.Register(instance, "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "alice-token"}))
	require.NoError(t, manager.Register(instance, "bob", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "bob-token"}))
	for _, tc := range []struct{ host, path, user, operation, want string }{
		{"example.test", "gitea/owner/repo.git", "", "get", "username=bob\npassword=bob-token\n\n"},
		{"example.test", "gitea/owner/repo.git", "alice", "get", "username=alice\npassword=alice-token\n\n"},
		{"example.test", "gitea/owner/repo.git", "unknown", "get", ""},
		{"other.test", "gitea/owner/repo.git", "alice", "get", ""},
		{"example.test", "other/owner/repo.git", "alice", "get", ""},
		{"example.test", "gitea/owner/repo.git", "alice", "store", ""},
		{"example.test", "gitea/owner/repo.git", "alice", "erase", ""},
	} {
		out.Reset()
		deps.Streams.In = strings.NewReader("protocol=https\nhost=" + tc.host + "\npath=" + tc.path + "\nusername=" + tc.user + "\npassword=replacement-token\n\n")
		require.NoError(t, Execute(context.Background(), deps, []string{"auth", "git-credential", tc.operation, "--no-input"}))
		require.Equal(t, tc.want, out.String())
		require.Empty(t, diagnostics.String())
	}
	credential, err := deps.Credentials.Load(instance, "alice", "file")
	require.NoError(t, err)
	require.Equal(t, "alice-token", credential.AccessToken)
	deps.Streams.In = strings.NewReader("protocol=https\nhost=example.test\npath=gitea/owner/repo.git\nusername=bob\n\n")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "git-credential", "get", "--user", "alice"}))
	require.Empty(t, out.String())
	err = Execute(context.Background(), deps, []string{"auth", "git-credential", "get", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Empty(t, out.String())
}

func TestGitCredentialHelperNeverEmitsInvalidStoredProtocolValues(t *testing.T) {
	deps, out, _ := testDeps(t)
	instance := "https://example.test"
	manager := testManager(t, deps)
	require.NoError(t, manager.Register(instance, "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "invalid-secret\nusername=other"}))
	deps.Streams.In = strings.NewReader("protocol=https\nhost=example.test\npath=owner/repo.git\n\n")
	err := Execute(context.Background(), deps, []string{"auth", "git-credential", "get"})
	require.Error(t, err)
	require.Equal(t, clierr.Authentication, clierr.ExitCode(err))
	require.NotContains(t, err.Error(), "invalid-secret")
	require.Empty(t, out.String())
}

func TestGitCredentialEnvironmentTokenIsScopedAndNeverPersisted(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatalf("unexpected request %s", r.URL.Path) }))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": server.URL, "GITEA_TOKEN": "environment-token"})
	host := strings.TrimPrefix(server.URL, "http://")
	deps.Streams.In = strings.NewReader("protocol=http\nhost=" + host + "\npath=alice/project.git\n\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "git-credential", "get"}))
	require.Equal(t, "username=alice\npassword=environment-token\n\n", out.String())
	files, err := os.ReadDir(deps.Settings.Dir)
	require.NoError(t, err)
	require.Empty(t, files)
	for _, requestHost := range []string{"unrelated.test", host} {
		out.Reset()
		username := ""
		if requestHost == host {
			username = "someone-else"
		}
		deps.Streams.In = strings.NewReader("protocol=http\nhost=" + requestHost + "\npath=alice/project.git\nusername=" + username + "\n\n")
		require.NoError(t, Execute(context.Background(), deps, []string{"auth", "git-credential", "get"}))
		require.Empty(t, out.String())
	}
}

func TestGitCredentialEnvironmentTokenAcceptsABareHost(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatalf("unexpected request %s", r.URL.Path) }))
	defer server.Close()
	deps, out, _ := testDeps(t)
	manager := testManager(t, deps)
	require.NoError(t, manager.Register(server.URL, "alice", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "stored-token"}))
	require.NoError(t, manager.Register(server.URL, "bob", "file", "token", auth.HostOptions{}, auth.Credential{AccessToken: "bob-token"}))
	host := strings.TrimPrefix(server.URL, "http://")
	deps.Getenv = envFunc(map[string]string{"GITEA_CLI_HOST": host, "GITEA_TOKEN": "environment-token"})
	deps.Streams.In = strings.NewReader("protocol=http\nhost=" + host + "\npath=alice/project.git\n\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"auth", "git-credential", "get"}))
	require.Equal(t, "username=alice\npassword=environment-token\n\n", out.String())
}
