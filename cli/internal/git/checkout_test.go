// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git/gittest"

	"github.com/stretchr/testify/require"
)

type checkoutRunner struct {
	calls            []string
	dirty, collision bool
	localCommit      string
	diverged         bool
	worktrees        string
	recordedPull     string
	// recordedHead is the head pr checkout recorded, which rewritten tells is not an ancestor of the new head.
	recordedHead string
	rewritten    bool
}

func (r *checkoutRunner) Output(_ context.Context, dir string, prompt bool, args ...string) (string, error) {
	if dir != "/work" || prompt {
		return "", errors.New("unexpected Git execution policy")
	}
	command := strings.Join(args, " ")
	r.calls = append(r.calls, command)
	switch args[0] {
	case "merge-base":
		if r.recordedHead != "" && args[2] == r.recordedHead {
			if r.rewritten {
				return "", &CommandError{ExitCode: 1}
			}
			return "", nil
		}
		if r.diverged {
			return "", &CommandError{ExitCode: 1}
		}
	case "rev-parse":
		if len(args) > 2 && strings.HasPrefix(args[2], "refs/heads/") && r.localCommit != "" {
			return r.localCommit, nil
		}
		if args[1] == "--is-inside-work-tree" {
			return "true", nil
		}
		return "abc123", nil
	case "worktree":
		return r.worktrees, nil
	case "config":
		if args[1] == "--get" && strings.HasSuffix(args[2], ".gitea-cli-pr") {
			return r.recordedPull, nil
		}
		if args[1] == "--get" && strings.HasSuffix(args[2], ".gitea-cli-pr-head") {
			return r.recordedHead, nil
		}
	case "status":
		if r.dirty {
			return "?? file", nil
		}
	case "for-each-ref":
		if r.collision {
			return "refs/heads/pr/7", nil
		}
	}
	return "", nil
}

func TestPullCheckoutValidatesBeforeChangingGitState(t *testing.T) {
	options := CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7"}
	for _, tc := range []struct {
		dirty, collision bool
		message          string
	}{{true, false, "working tree has changes"}, {false, true, "already exists"}} {
		runner := &checkoutRunner{dirty: tc.dirty, collision: tc.collision}
		_, err := CheckoutPull(context.Background(), runner, options)
		require.ErrorContains(t, err, tc.message)
		for _, call := range runner.calls {
			require.False(t, strings.HasPrefix(call, "fetch "))
			require.False(t, strings.HasPrefix(call, "switch "))
		}
	}
	runner := &checkoutRunner{}
	result, err := CheckoutPull(context.Background(), runner, options)
	require.NoError(t, err)
	require.Equal(t, CheckoutResult{Branch: "pr/7", Commit: "abc123", Remote: "origin"}, result)
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "check-ref-format --branch pr/7", "status --porcelain=v1 --untracked-files=no", "for-each-ref --format=%(refname) refs/heads/", "fetch --quiet --no-tags --refmap= -- origin refs/pull/7/head", "rev-parse --verify FETCH_HEAD^{commit}", "switch --quiet --create pr/7 abc123", "config branch.pr/7.gitea-cli-pr 7", "config branch.pr/7.gitea-cli-base-remote origin", "config branch.pr/7.gitea-cli-pr-head abc123"}, runner.calls)
}

func TestPullCheckoutRejectsChangedHead(t *testing.T) {
	runner := &checkoutRunner{}
	_, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "different"})
	require.ErrorContains(t, err, "PR head changed")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "switch "))
	}
}

func TestPullCheckoutScopesCredentialArgumentsToFetch(t *testing.T) {
	runner := &checkoutRunner{}
	arguments := []string{"-c", "credential.https://example.test.helper=!gt-helper"}
	_, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", CredentialArgs: arguments})
	require.NoError(t, err)
	credentialCalls := 0
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "-c ") {
			credentialCalls++
			require.Equal(t, "-c credential.https://example.test.helper=!gt-helper fetch --quiet --no-tags --refmap= -- origin refs/pull/7/head", call)
		}
	}
	require.Equal(t, 1, credentialCalls)
	require.Equal(t, []string{"-c", "credential.https://example.test.helper=!gt-helper"}, arguments)
}

func TestDetachedCheckoutDoesNotCreateOrChangeBranches(t *testing.T) {
	runner := &checkoutRunner{collision: true}
	result, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Detached: true, ExpectedCommit: "abc123"})
	require.NoError(t, err)
	require.Equal(t, CheckoutResult{Detached: true, Commit: "abc123", Remote: "origin"}, result)
	require.Equal(t, "switch --quiet --detach abc123", runner.calls[len(runner.calls)-1])
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "config "))
		require.False(t, strings.HasPrefix(call, "for-each-ref"))
		require.False(t, strings.HasPrefix(call, "check-ref-format"))
		require.NotContains(t, call, "--create")
	}
	runner = &checkoutRunner{dirty: true}
	_, err = CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Detached: true})
	require.ErrorContains(t, err, "working tree has changes")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "fetch "))
		require.False(t, strings.HasPrefix(call, "switch "))
	}
	runner = &checkoutRunner{}
	_, err = CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Detached: true, Branch: "existing"})
	require.ErrorContains(t, err, "cannot select a branch")
	require.Empty(t, runner.calls)
}

func TestPullCheckoutReusesMatchingBranch(t *testing.T) {
	runner := &checkoutRunner{collision: true}
	result, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123"})
	require.NoError(t, err)
	require.Equal(t, "abc123", result.Commit)
	require.Contains(t, runner.calls, "rev-parse --verify refs/heads/pr/7^{commit}")
	require.Equal(t, "switch --quiet pr/7", runner.calls[len(runner.calls)-4])
	runner = &checkoutRunner{collision: true, localCommit: "previous"}
	_, err = CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123"})
	require.EqualError(t, err, "branch pr/7 is behind pull request #7; pass --update to fast-forward it, or choose another checkout branch with --branch")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "switch "))
	}
}

func TestPullCheckoutUpdatesOnlyByFastForward(t *testing.T) {
	for _, diverged := range []bool{false, true} {
		runner := &checkoutRunner{collision: true, localCommit: "previous", diverged: diverged}
		_, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123", Update: true})
		require.Contains(t, runner.calls, "merge-base --is-ancestor previous abc123")
		if diverged {
			require.EqualError(t, err, "local branch pr/7 has commits that are not in pull request #7, so it cannot be fast-forwarded; choose another checkout branch with --branch, or reset it to the pull request, discarding those commits, with: git checkout -B pr/7 abc123")
			for _, call := range runner.calls {
				require.False(t, strings.HasPrefix(call, "switch "))
				require.False(t, strings.HasPrefix(call, "merge "))
			}
		} else {
			require.NoError(t, err)
			require.Equal(t, []string{"switch --quiet pr/7", "merge --ff-only --no-edit abc123"}, runner.calls[len(runner.calls)-5:len(runner.calls)-3])
		}
	}
}

func TestPullCheckoutReportsARewrittenPullRequest(t *testing.T) {
	runner := &checkoutRunner{collision: true, localCommit: "previous", diverged: true, recordedPull: "7", recordedHead: "previous", rewritten: true}
	_, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123", Update: true})
	require.EqualError(t, err, "pull request #7 was rewritten, such as by a force push, since branch pr/7 was checked out; to reset the branch to it, discarding the commits it no longer has and any local ones, run: git checkout -B pr/7 abc123")
	// Local commits on top of the recorded head are not a rewrite.
	runner = &checkoutRunner{collision: true, localCommit: "local", diverged: true, recordedPull: "7", recordedHead: "previous"}
	_, err = CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123", Update: true})
	require.ErrorContains(t, err, "local branch pr/7 has commits that are not in pull request #7")
}

func TestPullCheckoutTracksHeadBranch(t *testing.T) {
	runner := &checkoutRunner{}
	result, err := CheckoutPull(context.Background(), runner, CheckoutOptions{
		Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123",
		HeadRemote: "pulls/bob", HeadBranch: "feature",
	})
	require.NoError(t, err)
	require.Equal(t, CheckoutResult{Branch: "pr/7", Commit: "abc123", Remote: "pulls/bob", Tracking: "pulls/bob/feature"}, result)
	require.Contains(t, runner.calls, "check-ref-format refs/heads/feature")
	require.Contains(t, runner.calls, "fetch --quiet --no-tags --refmap= -- pulls/bob refs/heads/feature")
	require.Contains(t, runner.calls, "config branch.pr/7.remote pulls/bob")
	require.Contains(t, runner.calls, "config branch.pr/7.merge refs/heads/feature")
	require.Equal(t, []string{"config branch.pr/7.gitea-cli-pr 7", "config branch.pr/7.gitea-cli-base-remote origin", "config branch.pr/7.gitea-cli-pr-head abc123"}, runner.calls[len(runner.calls)-3:])
	for _, call := range runner.calls {
		require.NotContains(t, call, "refs/pull/")
	}
}

func TestPullCheckoutRejectsBranchCheckedOutInAnotherWorktree(t *testing.T) {
	options := CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123", Update: true}
	runner := &checkoutRunner{collision: true, localCommit: "abc123", worktrees: "worktree abc123\nHEAD 1\nbranch refs/heads/main\n\nworktree /other/tree\nHEAD 2\nbranch refs/heads/pr/7\n"}
	_, err := CheckoutPull(context.Background(), runner, options)
	require.ErrorContains(t, err, `branch "pr/7" is checked out in the worktree /other/tree`)
	require.ErrorContains(t, err, "--detach")
	for _, call := range runner.calls {
		require.NotContains(t, call, "fetch")
		require.NotContains(t, call, "switch")
	}
	// The current worktree holding the branch is fine.
	runner = &checkoutRunner{collision: true, localCommit: "abc123", worktrees: "worktree abc123\nHEAD 1\nbranch refs/heads/pr/7\n"}
	_, err = CheckoutPull(context.Background(), runner, options)
	require.NoError(t, err)
}

func TestPullCheckoutTracksAHeadBranchStartingWithADash(t *testing.T) {
	gittest.Isolate(t)
	remote, dir := t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run(remote, "init", "--quiet", "--bare")
	run(dir, "init", "--quiet", "--initial-branch=main")
	run(dir, "commit", "--quiet", "--allow-empty", "--message=x")
	run(dir, "remote", "add", "origin", remote)
	run(dir, "push", "--quiet", "origin", "main:refs/heads/-dash")
	result, err := CheckoutPull(context.Background(), System{}, CheckoutOptions{
		Directory: dir, Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: run(dir, "rev-parse", "main"),
		HeadRemote: "origin", HeadBranch: "-dash",
	})
	require.NoError(t, err)
	require.Equal(t, "origin/-dash", result.Tracking)
	require.Equal(t, "refs/heads/-dash", run(dir, "config", "branch.pr/7.merge"))
}

func TestPullCheckoutUpdateTellsLocalCommitsFromARewrite(t *testing.T) {
	gittest.Isolate(t)
	remote, dir := t.TempDir(), t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--bare", remote)
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "--message=base")
	run("remote", "add", "origin", remote)
	run("push", "--quiet", "origin", "main:refs/heads/feature")
	// push publishes a new commit on top of parent as the head branch.
	pushes := 0
	push := func(parent string) string {
		pushes++
		commit := run("commit-tree", "-p", parent, "-m", fmt.Sprint("remote ", pushes), parent+"^{tree}")
		run("push", "--quiet", "--force", "origin", commit+":refs/heads/feature")
		return commit
	}
	checkout := func(head string) error {
		_, err := CheckoutPull(context.Background(), System{}, CheckoutOptions{
			Directory: dir, Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: head, Update: true,
			HeadRemote: "origin", HeadBranch: "feature",
		})
		return err
	}
	base := run("rev-parse", "main")
	require.NoError(t, checkout(base))
	run("commit", "--quiet", "--allow-empty", "--message=local")
	local := "local branch pr/7 has commits that are not in pull request #7"
	require.NoError(t, checkout(base), "local commits on top of the head are kept")
	advanced := push(base)
	require.ErrorContains(t, checkout(advanced), local)
	run("reset", "--quiet", "--hard", advanced)
	require.NoError(t, checkout(advanced))
	require.ErrorContains(t, checkout(push(base)), "pull request #7 was rewritten")
}

func TestPullCheckoutSwitchesToABranchAheadOfTheHead(t *testing.T) {
	gittest.Isolate(t)
	remote, dir := t.TempDir(), t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--bare", remote)
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "--message=base")
	run("remote", "add", "origin", remote)
	head := run("rev-parse", "main")
	run("push", "--quiet", "origin", "main:refs/heads/feature")
	checkout := func(head string) (CheckoutResult, error) {
		return CheckoutPull(context.Background(), System{}, CheckoutOptions{
			Directory: dir, Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: head, HeadRemote: "origin", HeadBranch: "feature",
		})
	}
	_, err := checkout(head)
	require.NoError(t, err)
	run("commit", "--quiet", "--allow-empty", "--message=one")
	run("commit", "--quiet", "--allow-empty", "--message=two")
	tip := run("rev-parse", "HEAD")
	run("switch", "--quiet", "main")
	result, err := checkout(head)
	require.NoError(t, err)
	require.Equal(t, 2, result.Unpushed)
	require.Equal(t, "pr/7", run("branch", "--show-current"))
	require.Equal(t, tip, run("rev-parse", "HEAD"))

	run("switch", "--quiet", "main")
	run("push", "--quiet", "origin", tip+":refs/heads/feature")
	run("branch", "--quiet", "--force", "pr/7", head)
	_, err = checkout(tip)
	require.EqualError(t, err, "branch pr/7 is behind pull request #7; pass --update to fast-forward it, or choose another checkout branch with --branch")
	require.Equal(t, "main", run("branch", "--show-current"))
}

func TestPullCheckoutKeepsReportingARewriteOnRetry(t *testing.T) {
	gittest.Isolate(t)
	remote, dir := t.TempDir(), t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--bare", remote)
	run("init", "--quiet", "--initial-branch=main")
	run("commit", "--quiet", "--allow-empty", "--message=base")
	base := run("rev-parse", "main")
	run("remote", "add", "origin", remote)
	run("switch", "--quiet", "--create", "feature")
	run("commit", "--quiet", "--allow-empty", "--message=pushed")
	run("push", "--quiet", "--set-upstream", "origin", "feature")
	run("switch", "--quiet", "main")
	rewritten := run("commit-tree", "-p", base, "-m", "rewritten", base+"^{tree}")
	run("push", "--quiet", "origin", rewritten+":refs/heads/rewritten")
	run("--git-dir", remote, "update-ref", "refs/heads/feature", rewritten) // a push from here would move origin/feature
	for attempt := range 2 {
		_, err := CheckoutPull(context.Background(), System{}, CheckoutOptions{
			Directory: dir, Remote: "origin", Number: 7, Branch: "feature", ExpectedCommit: rewritten, Update: true,
			HeadRemote: "origin", HeadBranch: "feature",
		})
		require.ErrorContains(t, err, "pull request #7 was rewritten", "attempt %d", attempt)
	}
	run("branch", "--quiet", "--force", "feature", base)
	_, err := CheckoutPull(context.Background(), System{}, CheckoutOptions{
		Directory: dir, Remote: "origin", Number: 7, Branch: "feature", ExpectedCommit: rewritten, Update: true,
		HeadRemote: "origin", HeadBranch: "feature",
	})
	require.NoError(t, err)
	require.Equal(t, rewritten, run("rev-parse", "origin/feature"))
}
