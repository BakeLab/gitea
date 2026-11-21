// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/git/gittest"
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
	for preference, want := range map[string]string{"origin": "origin", "upstream": "tracking", "gone": "tracking"} {
		snapshot.Preferences.Remote = preference
		remote, err = a.checkoutRemote(context.Background(), target, snapshot)
		require.NoError(t, err, preference)
		require.Equal(t, want, remote, preference)
	}
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
		_, _ = fmt.Fprint(w, `{"number":7,"head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","ssh_url":"git@ssh.example.org:alice/project.git","permissions":{"push":true}}}}`)
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_INSTANCE_SSH_HOST": "ssh.example.org"})
	runner := &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": "git@ssh.example.org:alice/project.git",
		"rev-parse --is-inside-work-tree": "true", "check-ref-format --branch feature": "", "check-ref-format refs/heads/feature": "",
		"status --porcelain=v1 --untracked-files=no": "", "for-each-ref --format=%(refname) refs/heads/": "",
		"fetch --quiet --no-tags --refmap= -- origin refs/heads/feature": "", "rev-parse --verify FETCH_HEAD^{commit}": "abc123", "switch --quiet --create feature abc123": "",
		"config branch.feature.remote origin": "", "config branch.feature.merge refs/heads/feature": "",
		"config branch.feature.gitea-cli-pr 7": "", "config branch.feature.gitea-cli-base-remote origin": "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", "7", "--repo", "alice/project", "--remote", "origin", "--no-input", "--json"}))
	require.JSONEq(t, `{"branch":"feature","commit":"abc123","detached":false,"remote":"origin","tracking":"origin/feature"}`, out.String())

	maps.Copy(runner.results, map[string]string{
		"check-ref-format --branch local": "", "switch --quiet --create local abc123": "",
		"config branch.local.remote origin": "", "config branch.local.merge refs/heads/feature": "",
		"config branch.local.gitea-cli-pr 7": "", "config branch.local.gitea-cli-base-remote origin": "",
	})
	// Scripts need the push command as much as people do.
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", "7", "--repo", "alice/project", "--remote", "origin", "--branch", "local", "--no-input"}))
	require.Equal(t, "! Branch local is not named like the head branch; push to it with: git push origin HEAD:feature\n", diagnostics.String())
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
	fetch := strings.Join(append(sshArgs, "fetch", "--quiet", "--no-tags", "--refmap=", "--", "origin", "refs/heads/feature"), " ")
	runner := &fakeGit{results: map[string]string{
		"remote": "origin", "remote get-url origin": "ssh://git@git.example.org:2222/alice/project.git",
		"rev-parse --is-inside-work-tree": "true", "check-ref-format --branch feature": "", "check-ref-format refs/heads/feature": "",
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
		{"head name starting with a dash", services.Pull{Number: 7, Head: "-feature", HeadSHA: "abc123", HeadRepo: "alice/project"}, nil, "pr/7"},
		{"fork head name starting with a dash", services.Pull{Number: 7, Head: "-feature", HeadSHA: "abc123", HeadRepo: "bob/project"}, nil, "bob/-feature"},
		{"fork branch at the head without upstream", fork, map[string]string{"rev-parse --verify refs/heads/feature^{commit}": "abc123"}, "feature"},
		{"fork default branch tracking origin", services.Pull{Number: 7, Head: "main", HeadSHA: "abc123", HeadRepo: "bob/project"}, map[string]string{
			"rev-parse --verify refs/heads/main^{commit}": "abc123", "config --get branch.main.remote": "origin",
		}, "bob/main"},
		{"head name tracking the head behind it", same, map[string]string{
			"rev-parse --verify refs/heads/feature^{commit}": "old", "config --get branch.feature.remote": "origin",
			"config --get branch.feature.merge": "refs/heads/feature", "remote get-url origin": "https://example.org/alice/project.git",
		}, "feature"},
		{"fork head name tracking the fork's head", fork, map[string]string{
			"rev-parse --verify refs/heads/feature^{commit}": "abc123", "config --get branch.feature.remote": "origin",
			"config --get branch.feature.merge": "refs/heads/feature", "remote get-url origin": "https://example.org/bob/project.git",
		}, "feature"},
		{"fork head name tracking the base's namesake", fork, map[string]string{
			"rev-parse --verify refs/heads/feature^{commit}": "abc123", "config --get branch.feature.remote": "origin",
			"config --get branch.feature.merge": "refs/heads/feature", "remote get-url origin": "https://example.org/alice/project.git",
		}, "bob/feature"},
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
			require.Equal(t, tc.want, a.checkoutBranchName(context.Background(), target{Repo: "alice/project", Host: "https://example.org"}, tc.pull))
		})
	}
}

func TestCheckoutRejectsAnInvalidBranchBeforeRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	runner := &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true"}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "checkout", "7", "--branch", "bad..name", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "invalid branch name")
}

func TestCheckoutHeadRemoteFallsBackForANameGitRefuses(t *testing.T) {
	gittest.Isolate(t)
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init", "--quiet").CombinedOutput()
	require.NoError(t, err, string(out))
	deps, _, _ := testDeps(t)
	deps.Git = git.System{}
	a := &application{deps: deps, options: options{cwd: dir}}
	target := target{Host: "https://example.org", Repo: "alice/project"}
	for repo, want := range map[string]string{"bob/project": "pulls/bob/project", "bob/project.lock": ""} {
		pull := services.Pull{Number: 7, Head: "feature", HeadRepo: repo, HeadCloneURL: "https://example.org/" + repo + ".git"}
		name, _, undo, err := a.ensureHeadRemote(context.Background(), target, pull)
		require.NoError(t, err, repo)
		require.Equal(t, want, name, repo)
		if undo != nil {
			undo()
		}
	}
}

func TestCheckoutOffersAPushOnlyWhenItCanUpdateThePullRequest(t *testing.T) {
	const repo = `"repo":{"full_name":"alice/project","clone_url":"%[2]s/alice/project.git","permissions":{"push":%[3]t}}`
	for _, tc := range []struct {
		name, head, branch string
		push               bool
		want               string
		args               []string
	}{
		{"agit", `{"ref":"refs/pull/7/head","sha":%[1]q}`, "pr/7", true, "! Branch pr/7 is 1 commit ahead of the pull request\n", nil},
		{
			"deleted head", `{"ref":"refs/pull/7/head","label":"feature","sha":%[1]q}`, "pr/7", true,
			"! Branch pr/7 is 1 commit ahead of the pull request; its head branch was deleted, so a push cannot update it\n", nil,
		},
		{
			"head branch", `{"ref":"feature","label":"feature","sha":%[1]q,` + repo + `}`, "feature", true,
			"! Branch feature is 1 commit ahead of the pull request; push to update it\n", nil,
		},
		{
			"read-only head branch", `{"ref":"feature","label":"feature","sha":%[1]q,` + repo + `}`, "feature", false,
			"! Branch feature is 1 commit ahead of the pull request; you cannot push to alice/project\n", nil,
		},
		{
			"renamed head branch", `{"ref":"feature","label":"feature","sha":%[1]q,` + repo + `}`, "renamed", true,
			"! Branch renamed is not named like the head branch; push to it with: git push origin HEAD:feature\n" +
				"! Branch renamed is 1 commit ahead of the pull request; push to update it\n",
			[]string{"--branch", "renamed"},
		},
		{
			"read-only renamed head branch", `{"ref":"feature","label":"feature","sha":%[1]q,` + repo + `}`, "renamed", false,
			"! Branch renamed is 1 commit ahead of the pull request; you cannot push to alice/project\n",
			[]string{"--branch", "renamed"},
		},
	} {
		s := newBackportServer(t)
		s.git(s.pusher, "push", "--quiet", "origin", "feature")
		s.handle(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/api/v1/repos/alice/project/pulls/7" {
				return false
			}
			fmt.Fprintf(w, `{"number":7,"state":"open","head":`+tc.head+`,"base":{"ref":"main",`+repo+`}}`,
				s.pullCommits[1], s.server.URL, tc.push)
			return true
		})
		s.git(s.work, "fetch", "--quiet", "origin", "refs/pull/7/head")
		s.git(s.work, "switch", "--quiet", "-c", tc.branch, "FETCH_HEAD")
		s.git(s.work, "commit", "--quiet", "--allow-empty", "-m", "local")
		s.git(s.work, "config", "branch."+tc.branch+".gitea-cli-pr", "7")
		s.git(s.work, "switch", "--quiet", "work")
		_, diagnostics, err := s.run(append([]string{"pr", "checkout", "7", "--no-input"}, tc.args...)...)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, diagnostics, tc.name)
	}
}

func TestCheckoutRefusesAnExistingBranchOfNoPullRequest(t *testing.T) {
	s := newBackportServer(t)
	for _, branch := range []string{"main", "work"} {
		_, _, err := s.run("pr", "checkout", "7", "--branch", branch, "--no-input")
		require.EqualError(t, err, "branch "+branch+" already exists and is not of pull request #7; choose another checkout branch with --branch")
	}
	require.Equal(t, "refs/heads/main", s.git(s.work, "config", "--get", "branch.main.merge"))
	_, err := (git.System{}).Output(context.Background(), s.work, false, "config", "--get", "branch.main.gitea-cli-pr")
	require.Error(t, err)
}
