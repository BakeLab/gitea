// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestPullCleanRejectsOpenPullBeforeGitMutation(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"open","head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	runner := &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true"}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input"})
	require.ErrorContains(t, err, "pull request #7 is still open")
	require.Equal(t, []string{"rev-parse --is-inside-work-tree"}, runner.calls)
	require.Empty(t, out.String())
}

func TestPullCleanDeletesLocalAndRemoteBranches(t *testing.T) {
	open, ref := `[]`, "abc123"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/pulls":
			writeFirstPage(w, r, open)
		case "/api/v1/repos/alice/project/git/refs/pull/7/head":
			writePullRef(w, 7, ref)
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main","clone_url":"https://example.org/alice/project.git"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"title":"Add login","state":"closed","merged":true,"head":{"sha":"abc123","ref":"feature","label":"alice:feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","ssh_url":"ssh://git@example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	deleteRemote := credentialCall(t, deps, server.URL, "alice", "push", "--quiet", "--delete", "--force-with-lease=refs/heads/feature:abc123", "--", "origin", "refs/heads/feature")
	runner := &fakeGit{results: map[string]string{
		deleteRemote:                      "",
		"remote":                          "origin",
		"remote get-url origin":           "https://example.org/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"for-each-ref --format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename) refs/heads/": "abc123\tpr/7\torigin",
		"check-ref-format --branch pr/7":                      "",
		"check-ref-format --branch main":                      "",
		"status --porcelain=v1 --untracked-files=no":          "",
		"rev-parse --verify refs/heads/pr/7^{commit}":         "abc123",
		"rev-parse --verify --quiet refs/heads/main^{commit}": "mainsha", "worktree list --porcelain": "",
		"symbolic-ref --quiet HEAD": "pr/7",
		"switch --quiet -- main":    "",
		"branch -D -- pr/7":         "",
	}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--yes")
	assertNoMutation := func() {
		t.Helper()
		for _, call := range runner.calls {
			require.NotContains(t, call, "switch --")
			require.NotContains(t, call, "branch -D")
			require.NotContains(t, call, "push --delete")
		}
		require.Empty(t, out.String())
	}
	assertNoMutation()
	runner.calls = nil
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	err = Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Len(t, prompt.confirmations, 1)
	assertNoMutation()
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--json", "--yes"}))
	require.Contains(t, runner.calls, "branch -D -- pr/7")
	require.Contains(t, runner.calls, "switch --quiet -- main")
	require.Contains(t, runner.calls, deleteRemote)
	require.Contains(t, out.String(), `"local_branch":"pr/7"`)
	require.Contains(t, out.String(), `"remote_deleted":true`)

	// Deleting the branch would close the other pull request from it.
	open = `[{"number":8,"title":"Backport","state":"open","head":{"ref":"feature","repo":{"full_name":"alice/project"}}}]`
	runner.calls = nil
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--json", "--yes"}))
	require.Contains(t, runner.calls, "branch -D -- pr/7")
	require.NotContains(t, runner.calls, deleteRemote)
	require.Contains(t, out.String(), `"remote_skip_reason":"it is the head of open pull request alice/project#8 (Backport)"`)

	// Gitea reports the moved branch as the head of the closed pull request, and the local
	// branch found by it holds the new commits too.
	open, ref = `[]`, "def456"
	runner.calls = nil
	out.Reset()
	err = Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--yes"})
	require.EqualError(t, err, "branch feature has commits that are not in pull request alice/project#7 (Add login); delete it yourself if they are not needed")
	assertNoMutation()
}

func TestPullCleanRequiresIgnoreSHAWhenHeadMissingLocally(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"closed","head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":false}}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	runner := &fakeGit{results: map[string]string{
		"remote":                          "origin",
		"remote get-url origin":           "https://example.org/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"for-each-ref --format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename) refs/heads/": "deadbeef\tfeature\torigin/feature",
		"rev-parse --verify refs/remotes/origin/feature^{commit}":                                        "deadbeef",
	}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "no local branch is at commit abc123: the pull request was never checked out here, or the local branch diverged; retry with --ignore-sha to select it by name")
	require.Empty(t, out.String())
	// Git failing is no usage error.
	delete(runner.results, "for-each-ref --format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename) refs/heads/")
	err = Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input"})
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
}

func TestPullCleanFindsTheLocalBranchOfAnAGitPullRequestByItsCommit(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"closed","merged":true,"head":{"sha":"abc123","ref":"refs/pull/7/head","label":"","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	runner := &fakeGit{results: map[string]string{
		"remote":                          "origin",
		"remote get-url origin":           "https://example.org/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"for-each-ref --format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename) refs/heads/": "abc123\tpr/7\t",
		"check-ref-format --branch pr/7":                      "",
		"check-ref-format --branch main":                      "",
		"status --porcelain=v1 --untracked-files=no":          "",
		"rev-parse --verify refs/heads/pr/7^{commit}":         "abc123",
		"rev-parse --verify --quiet refs/heads/main^{commit}": "mainsha", "worktree list --porcelain": "",
		"symbolic-ref --quiet HEAD": "main",
		"branch -D -- pr/7":         "",
	}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--yes"}))
	require.Contains(t, runner.calls, "branch -D -- pr/7")
	for _, call := range runner.calls {
		require.NotContains(t, call, "refs/remotes/")
		require.NotContains(t, call, "push")
	}
	require.Empty(t, out.String())
	require.NotContains(t, diagnostics.String(), "emote branch")
}

func TestPullCleanOfAnAGitPullRequestWithADivergedBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"closed","merged":true,"head":{"sha":"abc123","ref":"refs/pull/7/head","label":"","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for _, test := range []struct {
		name, recorded string
		flags          []string
		want           string
	}{
		{name: "without --ignore-sha", want: "the local branch diverged"},
		{name: "with --ignore-sha and nothing recorded", flags: []string{"--ignore-sha"}, want: "no local branch was recorded for this AGit pull request"},
		{name: "with --ignore-sha and a recorded branch", recorded: "branch.topic.gitea-cli-pr 7", flags: []string{"--ignore-sha"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps, _, _ := serverDeps(t, server.URL)
			runner := &fakeGit{results: map[string]string{
				"remote":                          "origin",
				"remote get-url origin":           "https://example.org/alice/project.git",
				"rev-parse --is-inside-work-tree": "true",
				"for-each-ref --format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename) refs/heads/": "def456\ttopic\t",
				"check-ref-format --branch topic":                     "",
				"check-ref-format --branch main":                      "",
				"status --porcelain=v1 --untracked-files=no":          "",
				"rev-parse --verify refs/heads/topic^{commit}":        "def456",
				"rev-parse --verify --quiet refs/heads/main^{commit}": "mainsha", "worktree list --porcelain": "",
				"symbolic-ref --quiet HEAD": "main",
				"branch -D -- topic":        "",
			}}
			if test.recorded != "" {
				runner.results[`config --get-regexp ^branch\..*\.gitea-cli-pr$`] = test.recorded
			}
			deps.Git = runner
			err := Execute(context.Background(), deps, append([]string{"pr", "clean", "7", "--repo", "alice/project", "--yes"}, test.flags...))
			if test.want == "" {
				require.NoError(t, err)
				require.Contains(t, runner.calls, "branch -D -- topic")
				return
			}
			require.ErrorContains(t, err, test.want)
			require.NotContains(t, err.Error(), "--ignore-sha")
		})
	}
}

func TestPullCleanDeletesABranchBehindTheHeadAndTheRemoteCheckoutAdded(t *testing.T) {
	s := newBackportServer(t)
	s.git(s.work, "remote", "add", "pulls/bob/project", s.server.URL+"/alice/project.git")
	s.git(s.work, "fetch", "--quiet", "pulls/bob/project", "refs/pull/7/head:refs/remotes/pulls/bob/project/feature")
	s.git(s.work, "switch", "--quiet", "--create", "feature", s.pullCommits[0])
	s.git(s.work, "branch", "--quiet", "--set-upstream-to", "pulls/bob/project/feature")
	s.git(s.pusher, "commit", "--quiet", "--allow-empty", "-m", "later")
	s.git(s.pusher, "push", "--quiet", "origin", "HEAD:main")
	out, _, err := s.run("pr", "clean", "7", "--yes", "--json", "local_branch,removed_remote")
	require.NoError(t, err)
	require.JSONEq(t, `{"local_branch":"feature","removed_remote":"pulls/bob/project"}`, out)
	require.Equal(t, "origin", s.git(s.work, "remote"))
	require.Equal(t, "main", s.git(s.work, "branch", "--show-current"))
	require.Equal(t, s.git(s.pusher, "rev-parse", "HEAD"), s.git(s.work, "rev-parse", "main"))
}

// newHeadOnlyOnTheRemote moves the head of pull request #7 to a commit that only Gitea has, as
// gt pr update-branch or a push from another clone does, and checks out a branch behind it.
func newHeadOnlyOnTheRemote(t *testing.T) *backportServer {
	s := newBackportServer(t)
	s.git(s.pusher, "switch", "--quiet", "feature")
	s.pullCommits[1] = s.commit("three.txt", "three\n", "add three")
	s.git(s.pusher, "push", "--quiet", "--force", "origin", "HEAD:refs/pull/7/head")
	s.git(s.work, "switch", "--quiet", "--create", "feature", s.pullCommits[0])
	return s
}

func TestPullCleanFetchesAHeadTheCloneLacks(t *testing.T) {
	s := newHeadOnlyOnTheRemote(t)
	out, diagnostics, err := s.run("pr", "clean", "7", "--yes", "--json", "local_branch")
	require.NoError(t, err, diagnostics)
	require.JSONEq(t, `{"local_branch":"feature"}`, out)
	require.NotContains(t, diagnostics, "fatal:")
	require.Equal(t, "main", s.git(s.work, "branch", "--show-current"))
}

func TestPullMergeDeleteBranchFetchesAHeadTheCloneLacks(t *testing.T) {
	s := newHeadOnlyOnTheRemote(t)
	s.handle(func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/repos/alice/project/branches/feature":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			writeFirstPage(w, r, `[]`)
		default:
			return false
		}
		return true
	})
	_, diagnostics, err := s.run("pr", "merge", "7", "--delete-branch", "--yes")
	require.NoError(t, err, diagnostics)
	require.NotContains(t, diagnostics, "Kept local branch")
	require.NotContains(t, diagnostics, "fatal:")
	require.Empty(t, s.git(s.work, "branch", "--list", "feature"))
	require.Equal(t, "main", s.git(s.work, "branch", "--show-current"))
}
