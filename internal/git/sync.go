// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
)

// SyncOptions selects the local branch to fast-forward and the remote whose branch of the same name it follows.
type SyncOptions struct {
	Directory, Remote, Branch string
	CredentialArgs            []string
	Prompt                    bool
}

// SyncResult reports a synced branch. Previous is the branch's commit before the sync, empty when
// the sync created the branch; it equals Commit when there was nothing to sync. For a sync on the
// server, Remote names the repository the branch was synced from.
type SyncResult struct {
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
	Previous string `json:"previous"`
	Remote   string `json:"remote"`
}

// SyncBranch fast-forwards the local branch to the remote's branch of the same name, creating it
// when it is missing, and never changes which branch is checked out. The checked-out branch merges
// with --ff-only and needs a clean working tree; another branch moves with update-ref, which fails
// if the branch changed meanwhile. A branch that already contains the remote history is kept.
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
	ref := "refs/heads/" + opts.Branch
	head, err := optional(run("symbolic-ref", "--quiet", "HEAD"))
	if err != nil {
		return SyncResult{}, err
	}
	checkedOut := head == ref
	if checkedOut {
		status, err := run("status", "--porcelain=v1", "--untracked-files=normal")
		if err != nil {
			return SyncResult{}, err
		}
		if status != "" {
			return SyncResult{}, fmt.Errorf("working tree has changes; commit or stash them before syncing %s", opts.Branch)
		}
	}
	previous, err := optional(run("rev-parse", "--verify", "--quiet", ref+"^{commit}"))
	if err != nil {
		return SyncResult{}, err
	}
	fetch := append([]string{}, opts.CredentialArgs...)
	fetch = append(fetch, "fetch", "--quiet", "--no-tags", "--", opts.Remote, ref)
	if _, err := run(fetch...); err != nil {
		return SyncResult{}, err
	}
	commit, err := run("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{Branch: opts.Branch, Commit: commit, Previous: previous, Remote: opts.Remote}
	if previous == commit {
		return result, nil
	}
	if previous != "" {
		if _, err := run("merge-base", "--is-ancestor", previous, commit); err != nil {
			if failure, ok := errors.AsType[*CommandError](err); !ok || failure.ExitCode != 1 {
				return SyncResult{}, err
			}
			if _, err := run("merge-base", "--is-ancestor", commit, previous); err != nil {
				return SyncResult{}, fmt.Errorf("%s and %s/%s have diverged; merge or rebase before syncing: %w", opts.Branch, opts.Remote, opts.Branch, err)
			}
			result.Commit = previous
			return result, nil
		}
	}
	if !checkedOut {
		// The old value makes update-ref fail if the branch moved, or appeared, while fetching.
		if _, err := run("update-ref", "-m", "gt repo sync", ref, commit, previous); err != nil {
			return SyncResult{}, fmt.Errorf("%s changed while fetching; retry sync: %w", opts.Branch, err)
		}
		if previous == "" {
			for _, setting := range [][2]string{{"remote", opts.Remote}, {"merge", ref}} {
				if _, err := run("config", "branch."+opts.Branch+"."+setting[0], setting[1]); err != nil {
					return SyncResult{}, fmt.Errorf("created %s, but could not set it to track %s: %w", opts.Branch, opts.Remote, err)
				}
			}
		}
		return result, nil
	}
	current, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return SyncResult{}, err
	}
	if current != previous {
		return SyncResult{}, fmt.Errorf("%s changed while fetching; retry sync", opts.Branch)
	}
	if _, err := run("merge", "--ff-only", "--no-edit", commit); err != nil {
		return SyncResult{}, fmt.Errorf("could not fast-forward %s: %w", opts.Branch, err)
	}
	if actual, err := run("rev-parse", "--verify", "HEAD^{commit}"); err != nil || actual != commit {
		return SyncResult{}, errors.New("HEAD changed during sync; inspect the working tree before retrying")
	}
	return result, nil
}

// optional returns "" without an error for a Git command that exits with status 1 to report
// that what it looked for does not exist, such as rev-parse --verify --quiet.
func optional(output string, err error) (string, error) {
	if failure, ok := errors.AsType[*CommandError](err); ok && failure.ExitCode == 1 {
		return "", nil
	}
	return output, err
}
