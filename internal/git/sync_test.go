// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSyncValidatesBeforeChangingBranches(t *testing.T) {
	for _, test := range []struct {
		dirty, diverged bool
		message         string
	}{
		{true, false, "working tree has changes"},
		{false, true, "cannot be fast-forwarded"},
	} {
		runner := &checkoutRunner{dirty: test.dirty, diverged: test.diverged, localCommit: "previous"}
		_, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main"})
		require.ErrorContains(t, err, test.message)
		for _, call := range runner.calls {
			require.False(t, strings.HasPrefix(call, "switch "))
			require.False(t, strings.HasPrefix(call, "merge "))
			if test.dirty {
				require.False(t, strings.HasPrefix(call, "fetch "))
			}
		}
	}
	runner := &checkoutRunner{localCommit: "previous"}
	result, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main", CredentialArgs: []string{"-c", "credential.helper=tea"}})
	require.NoError(t, err)
	require.Equal(t, SyncResult{Branch: "main", Commit: "abc123", Remote: "origin"}, result)
	require.Contains(t, runner.calls, "-c credential.helper=tea fetch --no-tags -- origin refs/heads/main")
	require.Equal(t, []string{"switch main", "merge --ff-only --no-edit abc123", "rev-parse --verify HEAD^{commit}"}, runner.calls[len(runner.calls)-3:])
}

type syncFailureRunner struct {
	checkoutRunner
	missing, changed bool
	reads            int
}

func (r *syncFailureRunner) Output(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	if len(args) == 3 && args[0] == "rev-parse" && strings.HasPrefix(args[2], "refs/heads/") {
		r.reads++
		if r.missing {
			return "", &CommandError{ExitCode: 1}
		}
		if r.changed && r.reads == 2 {
			return "other-commit", nil
		}
	}
	return r.checkoutRunner.Output(ctx, dir, prompt, args...)
}

func TestSyncRejectsMissingOrChangedBranch(t *testing.T) {
	for _, test := range []struct {
		missing, changed bool
		message          string
	}{
		{true, false, "local branch is unavailable"},
		{false, true, "local branch changed while fetching"},
	} {
		runner := &syncFailureRunner{missing: test.missing, changed: test.changed}
		_, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main"})
		require.ErrorContains(t, err, test.message)
		for _, call := range runner.calls {
			require.False(t, strings.HasPrefix(call, "switch "))
			require.False(t, strings.HasPrefix(call, "merge "))
			if test.missing {
				require.False(t, strings.HasPrefix(call, "fetch "))
			}
		}
	}
}

type aheadSyncRunner struct{ checkoutRunner }

func (r *aheadSyncRunner) Output(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	if len(args) == 3 && args[2] == "HEAD^{commit}" {
		r.calls = append(r.calls, strings.Join(args, " "))
		return r.localCommit, nil
	}
	if len(args) == 4 && args[0] == "merge-base" && args[2] == "previous" {
		r.calls = append(r.calls, strings.Join(args, " "))
		return "", &CommandError{ExitCode: 1}
	}
	return r.checkoutRunner.Output(ctx, dir, prompt, args...)
}

func TestSyncPreservesBranchAheadOfRemote(t *testing.T) {
	runner := &aheadSyncRunner{localCommit: "previous"}
	result, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main"})
	require.NoError(t, err)
	require.Equal(t, "previous", result.Commit)
	require.Contains(t, runner.calls, "merge-base --is-ancestor abc123 previous")
	require.Equal(t, "switch main", runner.calls[len(runner.calls)-2])
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "merge "))
	}
}

func TestSyncEqualHistoryDoesNotMerge(t *testing.T) {
	runner := &checkoutRunner{}
	result, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main"})
	require.NoError(t, err)
	require.Equal(t, "abc123", result.Commit)
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "merge-base "))
		require.False(t, strings.HasPrefix(call, "merge "))
	}
}

type changedHeadRunner struct{ checkoutRunner }

func (r *changedHeadRunner) Output(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	if len(args) == 3 && args[2] == "HEAD^{commit}" {
		return "unexpected", nil
	}
	return r.checkoutRunner.Output(ctx, dir, prompt, args...)
}

func TestSyncRejectsUnexpectedFinalHead(t *testing.T) {
	runner := &changedHeadRunner{}
	result, err := SyncBranch(context.Background(), runner, SyncOptions{Directory: "/work", Remote: "origin", Branch: "main"})
	require.ErrorContains(t, err, "HEAD changed during sync")
	require.Equal(t, SyncResult{}, result)
}
