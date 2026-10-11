// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
)

// SyncOptions selects one existing local branch and its source remote branch.
type SyncOptions struct {
	Directory, Remote, Branch string
	CredentialArgs            []string
	Prompt                    bool
}

// SyncResult reports a completed local fast-forward.
type SyncResult struct {
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	Remote string `json:"remote"`
}

// SyncBranch fetches and fast-forwards an existing branch without resetting commits.
func SyncBranch(ctx context.Context, runner Runner, opts SyncOptions) (SyncResult, error) {
	run := func(args ...string) (string, error) { return runner.Output(ctx, opts.Directory, opts.Prompt, args...) }
	if opts.Remote == "" || opts.Branch == "" {
		return SyncResult{}, errors.New("sync requires a remote and branch")
	}
	if _, err := run("check-ref-format", "--branch", opts.Branch); err != nil {
		return SyncResult{}, err
	}
	if err := requireWorkTree(run, "sync"); err != nil {
		return SyncResult{}, err
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return SyncResult{}, err
	}
	if status != "" {
		return SyncResult{}, errors.New("working tree has changes; commit or stash them before syncing")
	}
	localRef := "refs/heads/" + opts.Branch + "^{commit}"
	previous, err := run("rev-parse", "--verify", localRef)
	if err != nil {
		return SyncResult{}, fmt.Errorf("local branch is unavailable; create it before syncing: %w", err)
	}
	fetch := append([]string{}, opts.CredentialArgs...)
	fetch = append(fetch, "fetch", "--no-tags", "--", opts.Remote, "refs/heads/"+opts.Branch)
	if _, err := run(fetch...); err != nil {
		return SyncResult{}, err
	}
	commit, err := run("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return SyncResult{}, err
	}
	current, err := run("rev-parse", "--verify", localRef)
	if err != nil {
		return SyncResult{}, err
	}
	if current != previous {
		return SyncResult{}, errors.New("local branch changed while fetching; retry sync")
	}
	if previous != commit {
		if _, err := run("merge-base", "--is-ancestor", previous, commit); err != nil {
			if failure, ok := errors.AsType[*CommandError](err); !ok || failure.ExitCode != 1 {
				return SyncResult{}, err
			}
			if _, err := run("merge-base", "--is-ancestor", commit, previous); err != nil {
				return SyncResult{}, fmt.Errorf("branch cannot be fast-forwarded; resolve divergence before syncing: %w", err)
			}
			// The local branch already contains the remote history.
			commit = previous
		}
	}
	if _, err := run("switch", opts.Branch); err != nil {
		return SyncResult{}, err
	}
	if previous != commit {
		if _, err := run("merge", "--ff-only", "--no-edit", commit); err != nil {
			return SyncResult{}, fmt.Errorf("branch was selected but sync failed: %w", err)
		}
	}
	actual, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync finished, but HEAD could not be verified: %w", err)
	}
	if actual != commit {
		return SyncResult{}, errors.New("HEAD changed during sync; inspect the working tree before retrying")
	}
	return SyncResult{Branch: opts.Branch, Commit: commit, Remote: opts.Remote}, nil
}
