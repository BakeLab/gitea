// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type checkoutRunner struct {
	calls            []string
	dirty, collision bool
	localCommit      string
	diverged         bool
	worktrees        string
}

func (r *checkoutRunner) Output(_ context.Context, dir string, prompt bool, args ...string) (string, error) {
	if dir != "/work" || prompt {
		return "", fmt.Errorf("unexpected Git execution policy")
	}
	command := strings.Join(args, " ")
	r.calls = append(r.calls, command)
	switch args[0] {
	case "merge-base":
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
	require.Equal(t, []string{"rev-parse --is-inside-work-tree", "check-ref-format --branch pr/7", "status --porcelain=v1 --untracked-files=normal", "for-each-ref --format=%(refname) refs/heads/", "fetch --quiet --no-tags -- origin refs/pull/7/head", "rev-parse --verify FETCH_HEAD^{commit}", "switch --quiet --create pr/7 abc123", "config branch.pr/7.gitea-cli-pr 7", "config branch.pr/7.gitea-cli-base-remote origin"}, runner.calls)
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
			require.Equal(t, "-c credential.https://example.test.helper=!gt-helper fetch --quiet --no-tags -- origin refs/pull/7/head", call)
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
	require.Equal(t, "switch --quiet pr/7", runner.calls[len(runner.calls)-3])
	runner = &checkoutRunner{collision: true}
	_, err = CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "different"})
	require.ErrorContains(t, err, "already exists with a different commit")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "fetch "))
		require.False(t, strings.HasPrefix(call, "switch "))
	}
}

func TestPullCheckoutUpdatesOnlyByFastForward(t *testing.T) {
	for _, diverged := range []bool{false, true} {
		runner := &checkoutRunner{collision: true, localCommit: "previous", diverged: diverged}
		_, err := CheckoutPull(context.Background(), runner, CheckoutOptions{Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123", Update: true})
		require.Contains(t, runner.calls, "merge-base --is-ancestor previous abc123")
		if diverged {
			require.ErrorContains(t, err, "cannot be fast-forwarded")
			for _, call := range runner.calls {
				require.False(t, strings.HasPrefix(call, "switch "))
				require.False(t, strings.HasPrefix(call, "merge "))
			}
		} else {
			require.NoError(t, err)
			require.Equal(t, []string{"switch --quiet pr/7", "merge --ff-only --no-edit abc123"}, runner.calls[len(runner.calls)-4:len(runner.calls)-2])
		}
	}
}

func TestPullCheckoutTracksHeadBranch(t *testing.T) {
	runner := &checkoutRunner{}
	result, err := CheckoutPull(context.Background(), runner, CheckoutOptions{
		Directory: "/work", Remote: "origin", Number: 7, Branch: "pr/7", ExpectedCommit: "abc123",
		HeadRemote: "pulls/bob", HeadBranch: "feature",
	})
	require.NoError(t, err)
	require.Equal(t, CheckoutResult{Branch: "pr/7", Commit: "abc123", Remote: "pulls/bob", Tracking: "pulls/bob/feature"}, result)
	require.Contains(t, runner.calls, "check-ref-format --branch feature")
	require.Contains(t, runner.calls, "fetch --quiet --no-tags -- pulls/bob refs/heads/feature")
	require.Contains(t, runner.calls, "config branch.pr/7.remote pulls/bob")
	require.Contains(t, runner.calls, "config branch.pr/7.merge refs/heads/feature")
	require.Equal(t, []string{"config branch.pr/7.gitea-cli-pr 7", "config branch.pr/7.gitea-cli-base-remote origin"}, runner.calls[len(runner.calls)-2:])
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
