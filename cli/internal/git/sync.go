// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// if the branch changed meanwhile. A branch that already contains the remote history is kept. A
// branch checked out in another linked worktree, or being rebased in any worktree, is refused,
// since moving it would leave that worktree's files behind its HEAD or break the rebase.
func SyncBranch(ctx context.Context, runner Runner, opts SyncOptions) (SyncResult, error) {
	run := func(args ...string) (string, error) { return runner.Output(ctx, opts.Directory, opts.Prompt, args...) }
	if opts.Remote == "" || opts.Branch == "" {
		return SyncResult{}, errors.New("sync requires a remote and branch")
	}
	if _, err := run("check-ref-format", "--branch", opts.Branch); err != nil {
		return SyncResult{}, fmt.Errorf("invalid branch name %q", opts.Branch)
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
	if !checkedOut {
		worktrees, err := run("worktree", "list", "--porcelain")
		if err != nil {
			return SyncResult{}, err
		}
		if path := worktreeWithBranch(worktrees, ref); path != "" {
			return SyncResult{}, fmt.Errorf("%s is checked out in the worktree %s; sync it from there", opts.Branch, path)
		}
		if path := rebasingWorktree(ctx, runner, worktrees, ref); path != "" {
			return SyncResult{}, fmt.Errorf("%s is being rebased in %s; finish or abort the rebase first", opts.Branch, path)
		}
	}
	if checkedOut {
		status, err := run("status", "--porcelain=v1", "--untracked-files=no")
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
		lsRemote := append(append([]string{}, opts.CredentialArgs...), "ls-remote", "--exit-code", "--heads", "--", opts.Remote, ref)
		if _, lsErr := run(lsRemote...); lsErr != nil {
			if failure, ok := errors.AsType[*CommandError](lsErr); ok && failure.ExitCode == 2 {
				return SyncResult{}, fmt.Errorf("branch %q not found on remote %s", opts.Branch, opts.Remote)
			}
		}
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
				return SyncResult{}, fmt.Errorf("%s and %s/%s have diverged; merge or rebase before syncing", opts.Branch, opts.Remote, opts.Branch)
			}
			result.Commit = previous
			return result, nil
		}
	}
	track := func() error {
		for _, setting := range [][2]string{{"remote", opts.Remote}, {"merge", ref}} {
			if _, err := run("config", "branch."+opts.Branch+"."+setting[0], setting[1]); err != nil {
				return fmt.Errorf("created %s, but could not set it to track %s: %w", opts.Branch, opts.Remote, err)
			}
		}
		return nil
	}
	if !checkedOut {
		// The old value makes update-ref fail if the branch moved, or appeared, while fetching.
		if _, err := run("update-ref", "-m", "gt repo sync", ref, commit, previous); err != nil {
			return SyncResult{}, fmt.Errorf("%s changed while fetching; retry sync: %w", opts.Branch, err)
		}
		if previous == "" {
			if err := track(); err != nil {
				return SyncResult{}, err
			}
		}
		return result, nil
	}
	// A checked-out branch without commits has no HEAD to compare; merge fast-forwards it.
	current, err := optional(run("rev-parse", "--verify", "--quiet", "HEAD^{commit}"))
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
	if previous == "" {
		if err := track(); err != nil {
			return SyncResult{}, err
		}
	}
	return result, nil
}

// worktreeWithBranch returns the path of the worktree in git worktree list --porcelain output
// that has ref checked out, or "" when none has.
func worktreeWithBranch(porcelain, ref string) string {
	path := ""
	for line := range strings.Lines(porcelain) {
		line = strings.TrimRight(line, "\r\n")
		if value, ok := strings.CutPrefix(line, "worktree "); ok {
			path = value
		} else if line == "branch "+ref {
			return path
		}
	}
	return ""
}

// rebasingWorktree returns the path of the worktree in worktree list --porcelain output whose
// detached HEAD is the result of rebasing ref, or "" when none is. Git records the branch being
// rebased in rebase-merge/head-name or rebase-apply/head-name of the worktree's Git directory.
func rebasingWorktree(ctx context.Context, runner Runner, porcelain, ref string) string {
	path := ""
	for line := range strings.Lines(porcelain) {
		line = strings.TrimRight(line, "\r\n")
		if value, ok := strings.CutPrefix(line, "worktree "); ok {
			path = value
		} else if line == "detached" {
			for _, name := range []string{"rebase-merge/head-name", "rebase-apply/head-name"} {
				file, err := runner.Output(ctx, path, false, "rev-parse", "--git-path", name)
				if err != nil {
					continue
				}
				if !filepath.IsAbs(file) {
					file = filepath.Join(path, file)
				}
				if head, err := os.ReadFile(file); err == nil && strings.TrimSpace(string(head)) == ref {
					return path
				}
			}
		}
	}
	return ""
}

// optional returns "" without an error for a Git command that exits with status 1 to report
// that what it looked for does not exist, such as rev-parse --verify --quiet.
func optional(output string, err error) (string, error) {
	if failure, ok := errors.AsType[*CommandError](err); ok && failure.ExitCode == 1 {
		return "", nil
	}
	return output, err
}
