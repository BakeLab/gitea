// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"strconv"
	"strings"
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
