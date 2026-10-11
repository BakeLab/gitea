// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// ErrAmbiguousRemote reports that repository discovery cannot choose between several remotes.
var ErrAmbiguousRemote = errors.New("ambiguous repository: select --remote or --repo")

// DiscoverRemote names the remote used for repository discovery: preferred when set, otherwise
// the current branch's upstream remote, then upstream, origin, or the only remote.
func DiscoverRemote(ctx context.Context, runner Runner, dir, preferred string) (string, error) {
	if preferred != "" {
		return preferred, nil
	}
	if _, remote := CurrentUpstreamRemote(ctx, runner, dir); remote != "" {
		return remote, nil
	}
	names, err := runner.Output(ctx, dir, false, "remote")
	if err != nil {
		return "", err
	}
	available := strings.Fields(names)
	for _, name := range []string{"upstream", "origin"} {
		if slices.Contains(available, name) {
			return name, nil
		}
	}
	if len(available) == 1 {
		return available[0], nil
	}
	return "", ErrAmbiguousRemote
}

// CurrentUpstreamRemote names the checked-out branch and the remote it tracks; each is "" when
// HEAD is detached or the branch tracks no remote.
func CurrentUpstreamRemote(ctx context.Context, runner Runner, dir string) (branch, remote string) {
	branch, err := runner.Output(ctx, dir, false, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", ""
	}
	return branch, UpstreamRemote(ctx, runner, dir, branch)
}

// UpstreamRemote names the remote branch tracks, or "" when it tracks none or a local branch.
// Unlike splitting @{upstream}, it keeps remote names with slashes, such as the
// pulls/<owner>/<repo> remotes pr checkout adds.
func UpstreamRemote(ctx context.Context, runner Runner, dir, branch string) string {
	if branch == "" {
		return ""
	}
	remote, err := runner.Output(ctx, dir, false, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "." {
		return ""
	}
	return remote
}
