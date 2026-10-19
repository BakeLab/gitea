// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestCheckoutRemoteUsesOnlyMatchingRepository(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "fork\norigin\nupstream\ntracking", "symbolic-ref --quiet HEAD": "main", "config --get branch.main.remote": "tracking",
		"remote get-url fork":     "https://example.org/bob/project.git",
		"remote get-url origin":   "https://example.org/alice/project.git",
		"remote get-url upstream": "https://other.example.org/alice/project.git",
		"remote get-url tracking": "https://example.org/alice/project.git",
	}}
	a := &application{deps: deps}
	snapshot := settings.Snapshot{Preferences: settings.Defaults()}
	target := target{Host: "https://example.org", Repo: "alice/project"}
	remote, err := a.checkoutRemote(context.Background(), target, snapshot)
	require.NoError(t, err)
	require.Equal(t, "tracking", remote)
	a.options.remote = "upstream"
	_, err = a.checkoutRemote(context.Background(), target, snapshot)
	require.ErrorContains(t, err, "does not match")
	a.options.remote = "origin"
	remote, err = a.checkoutRemote(context.Background(), target, snapshot)
	require.NoError(t, err)
	require.Equal(t, "origin", remote)
}

func TestCheckoutSelectedInstanceDisambiguatesSharedSSHRemote(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "local\norigin", "symbolic-ref --quiet HEAD": "main", "config --get branch.main.remote": "origin",
		"remote get-url local":  "/srv/git/project.git",
		"remote get-url origin": "ssh://git@git.example.org:2222/alice/project.git",
	}}
	a := &application{deps: deps}
	snapshot := settings.Snapshot{Preferences: settings.Defaults(), Hosts: settings.Hosts{Hosts: map[string]settings.Host{
		"https://example.org/first":  {SSHHost: "git.example.org:2222"},
		"https://example.org/second": {SSHHost: "git.example.org:2222"},
	}}}
	remote, err := a.checkoutRemote(context.Background(), target{Host: "https://example.org/second", Repo: "alice/project", host: snapshot.Hosts.Hosts["https://example.org/second"]}, snapshot)
	require.NoError(t, err)
	require.Equal(t, "origin", remote)
}

func TestCheckoutMatchesRemoteThroughEnvironmentSSHHost(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/7", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"number":7,"head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","ssh_url":"git@ssh.example.org:alice/project.git"}}}`)
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_INSTANCE_SSH_HOST": "ssh.example.org"})
	deps.Git = &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": "git@ssh.example.org:alice/project.git",
		"rev-parse --is-inside-work-tree": "true", "check-ref-format --branch feature": "",
		"status --porcelain=v1 --untracked-files=no": "", "for-each-ref --format=%(refname) refs/heads/": "",
		"fetch --quiet --no-tags -- origin refs/heads/feature": "", "rev-parse --verify FETCH_HEAD^{commit}": "abc123", "switch --quiet --create feature abc123": "",
		"config branch.feature.remote origin": "", "config branch.feature.merge refs/heads/feature": "",
		"config branch.feature.gitea-cli-pr 7": "", "config branch.feature.gitea-cli-base-remote origin": "",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", "7", "--repo", "alice/project", "--remote", "origin", "--no-input", "--json"}))
	require.JSONEq(t, `{"branch":"feature","commit":"abc123","detached":false,"remote":"origin","tracking":"origin/feature"}`, out.String())
}

func TestSSHCheckoutUsesAccountKeyOnlyForFetch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/7", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"number":7,"head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","ssh_url":"ssh://git@git.example.org:2222/alice/project.git","permissions":{"push":true}}}}`)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	configureHost(t, deps, server.URL, settings.Host{User: "alice", SSHHost: "git.example.org:2222", Users: map[string]settings.Account{
		"alice": {AuthMethod: "token", CredentialStore: "file", SSHKey: "/keys/alice key"},
		"bob":   {AuthMethod: "token", CredentialStore: "file", SSHKey: "/keys/bob"},
	}})
	sshArgs, err := git.SSHArguments("/keys/alice key", false)
	require.NoError(t, err)
	fetch := strings.Join(append(sshArgs, "fetch", "--quiet", "--no-tags", "--", "origin", "refs/heads/feature"), " ")
	runner := &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": "ssh://git@git.example.org:2222/alice/project.git",
		"rev-parse --is-inside-work-tree": "true", "check-ref-format --branch feature": "",
		"status --porcelain=v1 --untracked-files=no": "", "for-each-ref --format=%(refname) refs/heads/": "",
		fetch: "", "rev-parse --verify FETCH_HEAD^{commit}": "abc123", "switch --quiet --create feature abc123": "",
		"config branch.feature.remote origin": "", "config branch.feature.merge refs/heads/feature": "",
		"config branch.feature.gitea-cli-pr 7": "", "config branch.feature.gitea-cli-base-remote origin": "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", "7", "--repo", "alice/project", "--remote", "origin", "--no-input", "--json"}))
	require.Contains(t, runner.calls, fetch)
	require.Contains(t, runner.calls, "config branch.feature.remote origin")
	require.Contains(t, runner.calls, "config branch.feature.merge refs/heads/feature")
	configured := 0
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "-c ") {
			configured++
			require.Equal(t, fetch, call)
		}
	}
	require.Equal(t, 1, configured)
	require.False(t, runner.prompt)
	require.JSONEq(t, `{"branch":"feature","commit":"abc123","detached":false,"remote":"origin","tracking":"origin/feature"}`, out.String())
}

func TestCheckoutNamesTheBranchAfterTheHead(t *testing.T) {
	same := services.Pull{Number: 7, Head: "feature", HeadSHA: "abc123", HeadRepo: "alice/project"}
	fork := services.Pull{Number: 7, Head: "feature", HeadSHA: "abc123", HeadRepo: "bob/project"}
	for _, tc := range []struct {
		name     string
		pull     services.Pull
		branches map[string]string
		want     string
	}{
		{"free head name", same, nil, "feature"},
		{"head name at the head", same, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "abc123"}, "feature"},
		{"head name of an earlier checkout", same, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "old", "config --get branch.feature.gitea-cli-pr": "7"}, "feature"},
		{"head name taken", same, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "other"}, "pr/7"},
		{"fork head name taken", fork, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "other"}, "bob/feature"},
		{"fork names taken", fork, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "other", "rev-parse --verify refs/heads/bob/feature^{commit}": "other"}, "pr/7"},
		{"deleted head", services.Pull{Number: 7, Head: "feature", HeadRemoteDeleted: true}, nil, "pr/7"},
		{"fork branch at the head without upstream", fork, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "abc123"}, "feature"},
		{"fork default branch tracking origin", services.Pull{Number: 7, Head: "main", HeadSHA: "abc123", HeadRepo: "bob/project"}, map[string]string{
			"rev-parse --verify refs/heads/main^{commit}": "abc123", "config --get branch.main.remote": "origin",
		}, "bob/main"},
		{"branch above the head name", services.Pull{Number: 7, Head: "feature/login", HeadSHA: "abc123", HeadRepo: "alice/project"}, map[string]string{
			"rev-parse --verify refs/heads/feature^{commit}": "other",
		}, "pr/7"},
		{"branches below the head name", same, map[string]string{
			"for-each-ref --count=1 --format=%(refname) refs/heads/feature/": "refs/heads/feature/login",
		}, "pr/7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := testDeps(t)
			deps.Git = &fakeGit{results: tc.branches}
			a := &application{deps: deps}
			require.Equal(t, tc.want, a.checkoutBranchName(context.Background(), target{Repo: "alice/project"}, tc.pull))
		})
	}
}
