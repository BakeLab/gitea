// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Branch configuration keys that record the pull request and base remote of a checked-out PR branch.
const (
	pullNumberKey     = "gitea-cli-pr"
	pullBaseRemoteKey = "gitea-cli-base-remote"
)

// CheckoutOptions identifies the base remote and how to check out the PR commit.
type CheckoutOptions struct {
	// CredentialArgs contains per-operation Git configuration for the fetch only.
	CredentialArgs []string
	Directory      string
	Remote         string
	Number         int64
	Branch         string
	Detached       bool
	Update         bool
	Prompt         bool
	ExpectedCommit string
	// HeadRemote and HeadBranch select fork tracking when the head branch still exists.
	// When HeadBranch is empty, checkout fetches the retained pull ref from Remote.
	HeadRemote string
	HeadBranch string
}

// CheckoutResult describes the selected branch or detached PR commit.
type CheckoutResult struct {
	Detached bool   `json:"detached"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
	Remote   string `json:"remote,omitempty"`
	Tracking string `json:"tracking,omitempty"`
}

// CheckoutPull fetches a PR head and checks out its commit.
// When HeadRemote and HeadBranch are set, it fetches that head branch and configures tracking.
// Otherwise it fetches Gitea's retained pull ref from the base remote.
func CheckoutPull(ctx context.Context, runner Runner, options CheckoutOptions) (CheckoutResult, error) {
	if options.Number < 1 || options.Remote == "" || (!options.Detached && options.Branch == "") {
		return CheckoutResult{}, errors.New("checkout requires a pull number, base remote, and either a branch or detached mode")
	}
	if options.Detached && (options.Branch != "" || options.Update) {
		return CheckoutResult{}, errors.New("detached checkout cannot select a branch or update an existing branch")
	}
	if options.HeadBranch != "" && options.HeadRemote == "" {
		return CheckoutResult{}, errors.New("tracked checkout requires a head remote")
	}
	run := func(args ...string) (string, error) {
		return runner.Output(ctx, options.Directory, options.Prompt, args...)
	}
	if err := requireWorkTree(run, "PR checkout"); err != nil {
		return CheckoutResult{}, err
	}
	if !options.Detached {
		if _, err := run("check-ref-format", "--branch", options.Branch); err != nil {
			return CheckoutResult{}, fmt.Errorf("invalid branch name %q", options.Branch)
		}
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=no")
	if err != nil {
		return CheckoutResult{}, err
	}
	if status != "" {
		return CheckoutResult{}, errors.New("working tree has changes; commit or stash them before PR checkout")
	}
	existing := false
	previousCommit := ""
	if !options.Detached {
		branches, err := run("for-each-ref", "--format=%(refname)", "refs/heads/")
		if err != nil {
			return CheckoutResult{}, err
		}
		for branch := range strings.SplitSeq(branches, "\n") {
			if branch == "refs/heads/"+options.Branch {
				if options.ExpectedCommit == "" {
					return CheckoutResult{}, fmt.Errorf("branch %q already exists; choose another checkout branch", options.Branch)
				}
				commit, err := run("rev-parse", "--verify", branch+"^{commit}")
				if err != nil {
					return CheckoutResult{}, err
				}
				if commit != options.ExpectedCommit && !options.Update {
					return CheckoutResult{}, fmt.Errorf("branch %q already exists with a different commit; pass --update to fast-forward it, or choose another checkout branch with --branch", options.Branch)
				}
				previousCommit = commit
				existing = true
			}
		}
	}
	if existing {
		if err := requireBranchFreeElsewhere(run, options.Branch); err != nil {
			return CheckoutResult{}, err
		}
	}
	fetchRemote := options.Remote
	fetchRef := fmt.Sprintf("refs/pull/%d/head", options.Number)
	tracking := ""
	if options.HeadBranch != "" {
		if _, err := run("check-ref-format", "refs/heads/"+options.HeadBranch); err != nil {
			return CheckoutResult{}, fmt.Errorf("invalid head branch: %w", err)
		}
		fetchRemote = options.HeadRemote
		fetchRef = "refs/heads/" + options.HeadBranch
		tracking = options.HeadRemote + "/" + options.HeadBranch
	}
	fetchArgs := append([]string{}, options.CredentialArgs...)
	fetchArgs = append(fetchArgs, "fetch", "--quiet", "--no-tags", "--", fetchRemote, fetchRef)
	if _, err := run(fetchArgs...); err != nil {
		return CheckoutResult{}, err
	}
	commit, err := run("rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("PR was fetched, but its commit could not be resolved: %w", err)
	}
	if options.ExpectedCommit != "" && commit != options.ExpectedCommit {
		return CheckoutResult{}, errors.New("PR head changed while fetching; retry checkout")
	}
	if existing {
		localCommit, err := run("rev-parse", "--verify", "refs/heads/"+options.Branch+"^{commit}")
		if err != nil {
			return CheckoutResult{}, err
		}
		if localCommit != previousCommit {
			return CheckoutResult{}, errors.New("local branch changed while fetching; retry checkout")
		}
		if localCommit != commit {
			// merge-base --is-ancestor exits with status 1 to answer that it is not an ancestor.
			if _, err := run("merge-base", "--is-ancestor", localCommit, commit); err != nil {
				if failure, ok := errors.AsType[*CommandError](err); ok && failure.ExitCode == 1 {
					if ReadBranchConfig(ctx, runner, options.Directory, options.Branch).PullNumber == options.Number {
						return CheckoutResult{}, fmt.Errorf("pull request #%d was rewritten, such as by a force push, since branch %s was checked out; to drop the commits it no longer has, run: git checkout -B %s %s", options.Number, options.Branch, options.Branch, commit)
					}
					return CheckoutResult{}, fmt.Errorf("local branch %s has commits that pull request #%d does not have, so it cannot be fast-forwarded; choose another checkout branch with --branch, or drop them with: git checkout -B %s %s", options.Branch, options.Number, options.Branch, commit)
				}
				return CheckoutResult{}, err
			}
		}
	}
	switchArgs := []string{"switch", "--quiet", "--detach", commit}
	if !options.Detached {
		switchArgs = []string{"switch", "--quiet", "--create", options.Branch, commit}
		if existing {
			switchArgs = []string{"switch", "--quiet", options.Branch}
		}
	}
	if _, err := run(switchArgs...); err != nil {
		return CheckoutResult{}, fmt.Errorf("PR was fetched, but checkout failed: %w", err)
	}
	if existing && previousCommit != commit {
		if _, err := run("merge", "--ff-only", "--no-edit", commit); err != nil {
			return CheckoutResult{}, fmt.Errorf("checked out the existing branch, but its fast-forward failed: %w", err)
		}
	}
	if tracking != "" && !options.Detached {
		if _, err := run("config", "branch."+options.Branch+".remote", options.HeadRemote); err != nil {
			return CheckoutResult{}, fmt.Errorf("checked out the branch, but could not configure tracking: %w", err)
		}
		if _, err := run("config", "branch."+options.Branch+".merge", "refs/heads/"+options.HeadBranch); err != nil {
			return CheckoutResult{}, fmt.Errorf("checked out the branch, but could not configure tracking: %w", err)
		}
	}
	if !options.Detached {
		if err := RecordPull(ctx, runner, options.Directory, options.Branch, options.Number, options.Remote); err != nil {
			return CheckoutResult{}, fmt.Errorf("checked out the branch, but could not record its pull request: %w", err)
		}
	}
	return CheckoutResult{Branch: options.Branch, Commit: commit, Detached: options.Detached, Remote: fetchRemote, Tracking: tracking}, nil
}

// RecordPull records in the configuration of branch the pull request it belongs to and the remote
// of that pull request's base repository, which pr commands read to find the pull request of the
// branch and pr clean to find the branch of the pull request.
func RecordPull(ctx context.Context, runner Runner, dir, branch string, number int64, baseRemote string) error {
	for _, setting := range [][2]string{{pullNumberKey, strconv.FormatInt(number, 10)}, {pullBaseRemoteKey, baseRemote}} {
		if _, err := runner.Output(ctx, dir, false, "config", "branch."+branch+"."+setting[0], setting[1]); err != nil {
			return err
		}
	}
	return nil
}

// RecordBaseRemote records remote as the remote of the base repository of branch, which
// repository discovery prefers to the remote the branch tracks, such as a fork.
func RecordBaseRemote(ctx context.Context, runner Runner, dir, branch, remote string) error {
	_, err := runner.Output(ctx, dir, false, "config", "branch."+branch+"."+pullBaseRemoteKey, remote)
	return err
}

// requireBranchFreeElsewhere fails when branch is checked out in a linked worktree other than
// the current one, which git switch would refuse with a message that does not say where.
func requireBranchFreeElsewhere(run func(...string) (string, error), branch string) error {
	top, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	listing, err := run("worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if path := worktreeWithBranch(listing, "refs/heads/"+branch); path != "" && path != top {
		return fmt.Errorf("branch %q is checked out in the worktree %s; pass --detach to check out the pull request without a branch, or choose another branch with --branch", branch, path)
	}
	return nil
}
