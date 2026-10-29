// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// CleanOptions identifies a closed PR's local branch and optional remote head. RemoteSHA, when
// set, is the commit the remote head must still be at to be deleted.
type CleanOptions struct {
	Directory, DefaultBranch, LocalBranch, Remote, RemoteBranch, RemoteSHA string
	CredentialArgs                                                         []string
	Prompt, DeleteRemote                                                   bool
}

// CleanResult reports which branches were removed.
type CleanResult struct {
	LocalBranch      string `json:"local_branch"`
	Remote           string `json:"remote,omitempty"`
	RemoteBranch     string `json:"remote_branch,omitempty"`
	DefaultBranch    string `json:"default_branch"`
	RemoteDeleted    bool   `json:"remote_deleted"`
	RemoteSkipped    bool   `json:"remote_skipped"`
	RemoteSkipReason string `json:"remote_skip_reason,omitempty"`
	// RemovedRemote is the Git remote that pr checkout added for the branch, removed with it.
	RemovedRemote string `json:"removed_remote,omitempty"`
}

// CleanSelectionError reports that no single local branch of the pull request could be selected.
type CleanSelectionError struct{ message string }

func (e *CleanSelectionError) Error() string { return e.message }

// FindCleanBranchOptions searches for a local branch belonging to a closed PR.
type FindCleanBranchOptions struct {
	Directory, HeadSHA, HeadBranch  string
	DefaultBranch                   string
	PreferredNames, MatchingRemotes []string
	IgnoreSHA                       bool
	// PullNumber is the pull request being cleaned; a branch that pr checkout recorded for it
	// wins over branches that merely share the head branch's name.
	PullNumber int64
}

// FindCleanBranch locates one local branch for PR cleanup without mutating Git state.
func FindCleanBranch(ctx context.Context, runner Runner, opts FindCleanBranchOptions) (localBranch, remote string, err error) {
	run := func(args ...string) (string, error) {
		return runner.Output(ctx, opts.Directory, false, args...)
	}
	if err := requireWorkTree(run, "PR clean"); err != nil {
		return "", "", err
	}
	refs, err := run("for-each-ref", "--format=%(objectname)%09%(refname:lstrip=2)%09%(upstream:remotename)", "refs/heads/")
	if err != nil {
		return "", "", err
	}
	pullNumbers := recordedPullNumbers(ctx, runner, opts.Directory)
	type candidate struct {
		name, remote      string
		matched, recorded bool
	}
	setOf := func(names []string) map[string]bool {
		out := map[string]bool{}
		for _, name := range names {
			if name != "" {
				out[name] = true
			}
		}
		return out
	}
	behindHead := func(sha string) bool {
		behind, _ := ancestor(run, sha, opts.HeadSHA)
		return behind
	}
	preferred := setOf(opts.PreferredNames)
	matching := setOf(opts.MatchingRemotes)
	candidates := []candidate{}
	// named tells whether a branch would match by name, which --ignore-sha selects by.
	named := false
	for line := range strings.SplitSeq(refs, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		sha, name := parts[0], parts[1]
		if name == opts.DefaultBranch {
			continue
		}
		upstream := ""
		if len(parts) == 3 {
			upstream = parts[2]
		}
		recorded := opts.PullNumber > 0 && pullNumbers[name] == opts.PullNumber
		byName := (opts.HeadBranch != "" && name == opts.HeadBranch) || preferred[name] || recorded
		named = named || byName
		if opts.IgnoreSHA {
			if !byName {
				continue
			}
		} else if opts.HeadSHA == "" || (sha != opts.HeadSHA && (!byName || !behindHead(sha))) {
			// A branch of the pull request that is behind its head has no commits of its own.
			continue
		}
		remoteName, matched := "", matching[upstream]
		if matched {
			remoteName = upstream
		}
		// An AGit pull request has no head branch to look up among the remote-tracking branches.
		if !matched && opts.HeadBranch != "" {
			for remote := range matching {
				tracking, trackErr := run("rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"/"+opts.HeadBranch+"^{commit}")
				if trackErr == nil && (opts.IgnoreSHA || tracking == sha) {
					remoteName, matched = remote, true
					break
				}
			}
		}
		if !matched && len(matching) == 1 {
			for remote := range matching {
				remoteName = remote
			}
		}
		candidates = append(candidates, candidate{name: name, remote: remoteName, matched: matched, recorded: recorded})
	}
	if len(candidates) == 0 {
		switch {
		case opts.IgnoreSHA && opts.HeadBranch == "":
			return "", "", &CleanSelectionError{"no local branch was recorded for this AGit pull request; it has no head branch to select by name"}
		case opts.IgnoreSHA:
			return "", "", &CleanSelectionError{fmt.Sprintf("local branch %q was not found for the selected pull request", opts.HeadBranch)}
		case opts.HeadBranch == "" && !named:
			// An AGit pull request has no head branch name to retry with.
			return "", "", &CleanSelectionError{fmt.Sprintf("no local branch is at commit %s: the pull request was never checked out here, or the local branch diverged", shortCommit(opts.HeadSHA))}
		}
		return "", "", &CleanSelectionError{fmt.Sprintf("no local branch is at commit %s: the pull request was never checked out here, or the local branch diverged; retry with --ignore-sha to select it by name", shortCommit(opts.HeadSHA))}
	}
	selected := []candidate{}
	for _, item := range candidates {
		if item.matched || item.recorded || preferred[item.name] || item.name == opts.HeadBranch {
			selected = append(selected, item)
		}
	}
	// Only an AGit pull request, which has no head branch, selects a branch by its commit alone.
	if len(selected) == 0 && opts.HeadBranch == "" {
		selected = candidates
	}
	if len(selected) == 0 {
		names := make([]string, 0, len(candidates))
		for _, item := range candidates {
			names = append(names, item.name)
		}
		return "", "", &CleanSelectionError{fmt.Sprintf("local branch %q was not found, and the branches at its head commit (%s) neither have its name nor track it; rename the one to clean to %s", opts.HeadBranch, strings.Join(names, ", "), opts.HeadBranch)}
	}
	if recorded := slices.DeleteFunc(slices.Clone(selected), func(item candidate) bool { return !item.recorded }); len(recorded) > 0 {
		selected = recorded
	}
	if len(selected) > 1 {
		strong := make([]candidate, 0, len(selected))
		for _, item := range selected {
			if item.matched {
				strong = append(strong, item)
			}
		}
		if len(strong) > 0 {
			selected = strong
		}
	}
	if len(selected) != 1 {
		names := make([]string, 0, len(selected))
		for _, item := range selected {
			names = append(names, item.name)
		}
		return "", "", &CleanSelectionError{fmt.Sprintf("multiple local branches match this pull request (%s); check out or rename until one remains", strings.Join(names, ", "))}
	}
	return selected[0].name, selected[0].remote, nil
}

// CleanPull deletes the selected local branch and optionally its remote head.
func CleanPull(ctx context.Context, runner Runner, opts CleanOptions) (CleanResult, error) {
	run := func(args ...string) (string, error) {
		return runner.Output(ctx, opts.Directory, opts.Prompt, args...)
	}
	if opts.LocalBranch == "" || opts.DefaultBranch == "" {
		return CleanResult{}, errors.New("clean requires a local branch and repository default branch")
	}
	if opts.LocalBranch == opts.DefaultBranch {
		return CleanResult{}, errors.New("refusing to delete the repository default branch")
	}
	if opts.DeleteRemote && (opts.Remote == "" || opts.RemoteBranch == "") {
		return CleanResult{}, errors.New("remote deletion requires a remote and branch name")
	}
	if err := requireWorkTree(run, "PR clean"); err != nil {
		return CleanResult{}, err
	}
	if _, err := run("check-ref-format", "--branch", opts.LocalBranch); err != nil {
		return CleanResult{}, fmt.Errorf("invalid local branch: %w", err)
	}
	if _, err := run("check-ref-format", "--branch", opts.DefaultBranch); err != nil {
		return CleanResult{}, fmt.Errorf("invalid default branch: %w", err)
	}
	status, err := run("status", "--porcelain=v1", "--untracked-files=no")
	if err != nil {
		return CleanResult{}, err
	}
	if status != "" {
		return CleanResult{}, errors.New("working tree has changes; commit or stash them before cleaning")
	}
	if _, err := run("rev-parse", "--verify", "refs/heads/"+opts.LocalBranch+"^{commit}"); err != nil {
		return CleanResult{}, fmt.Errorf("local branch is unavailable: %w", err)
	}
	current, err := CurrentBranch(ctx, runner, opts.Directory)
	if err == nil && current == opts.LocalBranch {
		if err := switchAway(run, opts.DefaultBranch); err != nil {
			return CleanResult{}, fmt.Errorf("could not switch to %s before deleting %s: %w", opts.DefaultBranch, opts.LocalBranch, err)
		}
	}
	tracked, _ := run("config", "--get", "branch."+opts.LocalBranch+".remote")
	if _, err := run("branch", "-D", "--", opts.LocalBranch); err != nil {
		return CleanResult{}, fmt.Errorf("local branch could not be deleted: %w", err)
	}
	result := CleanResult{LocalBranch: opts.LocalBranch, DefaultBranch: opts.DefaultBranch}
	if opts.DeleteRemote {
		push := append([]string{}, opts.CredentialArgs...)
		push = append(push, "push", "--quiet", "--delete")
		if opts.RemoteSHA != "" {
			push = append(push, "--force-with-lease=refs/heads/"+opts.RemoteBranch+":"+opts.RemoteSHA)
		}
		push = append(push, "--", opts.Remote, "refs/heads/"+opts.RemoteBranch)
		if _, err := run(push...); err != nil {
			return result, fmt.Errorf("deleted local branch %s, but deleting remote branch %s failed: %w", opts.LocalBranch, opts.RemoteBranch, err)
		}
		result.Remote, result.RemoteBranch, result.RemoteDeleted = opts.Remote, opts.RemoteBranch, true
	} else {
		result.RemoteSkipped = true
		result.RemoteSkipReason = "remote head was already deleted or push is unavailable"
	}
	result.RemovedRemote = removeCheckoutRemote(run, tracked)
	return result, nil
}

// removeCheckoutRemote removes remote, returning its name, when it is one that pr checkout added
// for a fork (pulls/OWNER/REPO) and no branch tracks it anymore.
func removeCheckoutRemote(run func(...string) (string, error), remote string) string {
	if !strings.HasPrefix(remote, "pulls/") {
		return ""
	}
	listing, err := run("config", "--get-regexp", `^branch\..*\.remote$`)
	if failure, ok := errors.AsType[*CommandError](err); err != nil && (!ok || failure.ExitCode != 1) { // 1: no branch tracks a remote
		return ""
	}
	for line := range strings.Lines(listing) {
		if _, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " "); value == remote {
			return ""
		}
	}
	if _, err := run("remote", "remove", "--", remote); err != nil {
		return ""
	}
	return remote
}

// switchAway switches to branch: detached at it when another worktree has it checked out, and
// created from its only remote-tracking branch when it exists only there.
func switchAway(run func(...string) (string, error), branch string) error {
	ref := "refs/heads/" + branch
	if _, err := run("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		tracking, err := run("for-each-ref", "--format=%(refname)", "refs/remotes/*/"+branch)
		if err != nil || tracking == "" || strings.Contains(tracking, "\n") {
			return fmt.Errorf("branch %s is unavailable locally; create or fetch it before cleaning", branch)
		}
		_, err = run("switch", "--quiet", "--track", "--create", branch, tracking)
		return err
	}
	listing, err := run("worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if worktreeWithBranch(listing, ref) != "" {
		_, err = run("switch", "--quiet", "--detach", ref)
		return err
	}
	_, err = run("switch", "--quiet", "--", branch)
	return err
}

// recordedPullNumbers returns the pull request number that gt pr checkout recorded for each local
// branch, read in one git call.
func recordedPullNumbers(ctx context.Context, runner Runner, dir string) map[string]int64 {
	numbers := map[string]int64{}
	listing, err := runner.Output(ctx, dir, false, "config", "--get-regexp", `^branch\..*\.`+regexp.QuoteMeta(pullNumberKey)+`$`)
	if err != nil {
		return numbers
	}
	for line := range strings.Lines(listing) {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		name := strings.TrimSuffix(strings.TrimPrefix(key, "branch."), "."+pullNumberKey)
		if n, err := strconv.ParseInt(value, 10, 64); err == nil && n > 0 {
			numbers[name] = n
		}
	}
	return numbers
}
