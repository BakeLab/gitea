// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrNotOnBranch reports a detached HEAD or a directory outside a checkout.
var ErrNotOnBranch = errors.New("not on a branch")

// BranchConfig is the configuration of a local branch that locates its pull request.
type BranchConfig struct {
	// Name is the local branch.
	Name string
	// Remote and Merge are the remote and branch the branch tracks, Merge without refs/heads/;
	// both are empty for a branch that tracks nothing or another local branch.
	Remote, Merge string
	// PullNumber and BaseRemote are what pr checkout recorded: the pull request the branch was
	// checked out from and the remote of that pull request's base repository.
	PullNumber int64
	BaseRemote string
}

// CurrentBranchConfig reads the configuration of the checked-out branch, failing with
// ErrNotOnBranch when HEAD is detached or dir is not a checkout.
func CurrentBranchConfig(ctx context.Context, runner Runner, dir string) (BranchConfig, error) {
	name, err := CurrentBranch(ctx, runner, dir)
	if err != nil {
		return BranchConfig{}, err
	}
	return ReadBranchConfig(ctx, runner, dir, name), nil
}

// CurrentBranch names the checked-out branch, failing with ErrNotOnBranch when HEAD is detached
// or dir is not a checkout. It reads the full ref because --short answers heads/NAME for a branch
// that shares its name with a tag.
func CurrentBranch(ctx context.Context, runner Runner, dir string) (string, error) {
	ref, err := runner.Output(ctx, dir, false, "symbolic-ref", "--quiet", "HEAD")
	name := strings.TrimPrefix(ref, "refs/heads/")
	if err != nil || name == "" {
		return "", ErrNotOnBranch
	}
	return name, nil
}

// ReadBranchConfig reads branch.<name>.remote, .merge and the keys pr checkout records.
// Missing or malformed keys stay empty.
func ReadBranchConfig(ctx context.Context, runner Runner, dir, name string) BranchConfig {
	get := func(key string) string {
		value, err := runner.Output(ctx, dir, false, "config", "--get", "branch."+name+"."+key)
		if err != nil {
			return ""
		}
		return value
	}
	config := BranchConfig{Name: name, Remote: get("remote"), BaseRemote: get(pullBaseRemoteKey)}
	if config.Remote == "." {
		config.Remote = ""
	}
	if config.Remote != "" {
		config.Merge = strings.TrimPrefix(get("merge"), "refs/heads/")
	}
	if n, err := strconv.ParseInt(get(pullNumberKey), 10, 64); err == nil && n > 0 {
		config.PullNumber = n
	}
	return config
}

// LocalBranch is a local branch with the configuration that locates its pull request.
type LocalBranch struct {
	BranchConfig
	Commit  string
	Current bool
	// Committed and UpstreamCommitted are the committer dates of the branch's tip and of the
	// remote-tracking branch it tracks, which is zero when there is none.
	Committed, UpstreamCommitted time.Time
}

// LocalBranches lists the local branches of the clone in dir by name, in four git calls.
func LocalBranches(ctx context.Context, runner Runner, dir string) ([]LocalBranch, error) {
	run := func(args ...string) (string, error) {
		return runner.Output(ctx, dir, false, args...)
	}
	refs, err := run("for-each-ref", "--format=%(objectname)%09%(HEAD)%09%(committerdate:unix)%09%(upstream:remotename)%09%(upstream:remoteref)%09%(upstream)%09%(refname:lstrip=2)", "refs/heads/") // Output trims a leading %(HEAD) blank
	if err != nil {
		return nil, err
	}
	tracking := map[string]time.Time{}
	if listing, err := run("for-each-ref", "--format=%(committerdate:unix)%09%(refname)", "refs/remotes/"); err == nil {
		for line := range strings.Lines(listing) {
			date, ref, _ := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
			tracking[ref] = unixTime(date)
		}
	}
	numbers := recordedPullNumbers(ctx, runner, dir)
	bases := branchConfigValues(ctx, runner, dir, pullBaseRemoteKey)
	var branches []LocalBranch
	for line := range strings.Lines(refs) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
		if len(fields) != 7 {
			continue
		}
		name := fields[6]
		branch := LocalBranch{
			BranchConfig: BranchConfig{Name: name, PullNumber: numbers[name], BaseRemote: bases[name]},
			Commit:       fields[0], Current: fields[1] == "*", Committed: unixTime(fields[2]),
		}
		if remote := fields[3]; remote != "" && remote != "." {
			branch.Remote, branch.Merge = remote, strings.TrimPrefix(fields[4], "refs/heads/")
			branch.UpstreamCommitted = tracking[fields[5]]
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

func unixTime(seconds string) time.Time {
	n, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(n, 0)
}
