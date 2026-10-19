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

// CleanOptions identifies a closed PR's local branch and optional remote head.
type CleanOptions struct {
	Directory, DefaultBranch, LocalBranch, Remote, RemoteBranch string
	CredentialArgs                                              []string
	Prompt, DeleteRemote                                        bool
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
}

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
	refs, err := run("for-each-ref", "--format=%(objectname)%09%(refname:short)%09%(upstream:remotename)", "refs/heads/")
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
	preferred := setOf(opts.PreferredNames)
	matching := setOf(opts.MatchingRemotes)
	candidates := []candidate{}
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
		if opts.IgnoreSHA {
			if name != opts.HeadBranch && !preferred[name] && !recorded {
				continue
			}
		} else if opts.HeadSHA == "" || sha != opts.HeadSHA {
			continue
		}
		remoteName, matched := upstream, matching[upstream]
		if !matched {
			for remote := range matching {
				tracking, trackErr := run("rev-parse", "--verify", "refs/remotes/"+remote+"/"+opts.HeadBranch+"^{commit}")
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
		if opts.IgnoreSHA {
			return "", "", fmt.Errorf("local branch %q was not found for the selected pull request", opts.HeadBranch)
		}
		return "", "", fmt.Errorf(`local branch for commit %s was not found.
Either this pull request was never checked out, or the local branch diverged.
Retry with --ignore-sha to select by branch name`, opts.HeadSHA)
	}
	selected := []candidate{}
	for _, item := range candidates {
		if item.matched || item.recorded || preferred[item.name] || item.name == opts.HeadBranch {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		selected = candidates
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
		return "", "", fmt.Errorf("multiple local branches match this pull request (%s); check out or rename until one remains", strings.Join(names, ", "))
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
	if _, err := run("rev-parse", "--verify", "refs/heads/"+opts.DefaultBranch+"^{commit}"); err != nil {
		return CleanResult{}, fmt.Errorf("default branch is unavailable; create or fetch it before cleaning: %w", err)
	}
	current, err := CurrentBranch(ctx, runner, opts.Directory)
	if err == nil && current == opts.LocalBranch {
		if _, err := run("switch", "--quiet", "--", opts.DefaultBranch); err != nil {
			return CleanResult{}, fmt.Errorf("could not switch to %s before deleting %s: %w", opts.DefaultBranch, opts.LocalBranch, err)
		}
	}
	if _, err := run("branch", "-D", "--", opts.LocalBranch); err != nil {
		return CleanResult{}, fmt.Errorf("local branch could not be deleted: %w", err)
	}
	result := CleanResult{LocalBranch: opts.LocalBranch, DefaultBranch: opts.DefaultBranch}
	if !opts.DeleteRemote {
		result.RemoteSkipped = true
		result.RemoteSkipReason = "remote head was already deleted or push is unavailable"
		return result, nil
	}
	push := append([]string{}, opts.CredentialArgs...)
	push = append(push, "push", "--delete", "--", opts.Remote, opts.RemoteBranch)
	if _, err := run(push...); err != nil {
		return result, fmt.Errorf("deleted local branch %s, but deleting remote branch %s failed: %w", opts.LocalBranch, opts.RemoteBranch, err)
	}
	result.Remote, result.RemoteBranch, result.RemoteDeleted = opts.Remote, opts.RemoteBranch, true
	return result, nil
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
