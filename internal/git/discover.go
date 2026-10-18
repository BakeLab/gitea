// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
)

// ErrAmbiguousRemote reports that repository discovery cannot choose between several remotes.
var ErrAmbiguousRemote = errors.New("ambiguous repository: select --remote or --repo")

// DiscoverRemote names the remote used for repository discovery: preferred when set, otherwise
// the base remote pr checkout recorded for the current branch, the current branch's upstream
// remote unless it is a pulls/<owner>/<repo> head remote, then upstream, origin, or the only remote.
// When repository is not nil, it names the repository of a remote gt can act on, or returns ""
// for one on another forge. Those steps then skip such remotes, and the other remotes decide when
// they name exactly one repository; with none of them usable the choice is the name-based one.
func DiscoverRemote(ctx context.Context, runner Runner, dir, preferred string, repository func(remote string) string) (string, error) {
	if preferred != "" {
		return preferred, nil
	}
	usable := func(remote string) bool { return repository == nil || repository(remote) != "" }
	// fallback is the remote chosen by name alone, used when no remote is usable.
	var fallback string
	var checked []string
	try := func(remote string) bool {
		if remote == "" || slices.Contains(checked, remote) {
			return false
		}
		if usable(remote) {
			return true
		}
		fallback = cmp.Or(fallback, remote)
		checked = append(checked, remote)
		return false
	}
	branch, tracking := CurrentUpstreamRemote(ctx, runner, dir)
	if strings.HasPrefix(tracking, "pulls/") {
		tracking = ""
	}
	for _, remote := range []string{pullBaseRemote(ctx, runner, dir, branch), tracking} {
		if try(remote) {
			return remote, nil
		}
	}
	names, err := runner.Output(ctx, dir, false, "remote")
	if err != nil {
		if fallback != "" {
			return fallback, nil
		}
		return "", err
	}
	available := strings.Fields(names)
	for _, name := range []string{"upstream", "origin"} {
		if slices.Contains(available, name) && try(name) {
			return name, nil
		}
	}
	if repository != nil {
		var found, foundRepository string
		for _, name := range available {
			// pr checkout's head remotes point at forks; they never become the discovered repository.
			if slices.Contains(checked, name) || strings.HasPrefix(name, "pulls/") {
				continue
			}
			switch repo := repository(name); {
			case repo == "":
			case found == "":
				found, foundRepository = name, repo
			case repo != foundRepository:
				return "", ErrAmbiguousRemote
			}
		}
		if found != "" {
			return found, nil
		}
	}
	if fallback != "" {
		return fallback, nil
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

// pullBaseRemote returns the base remote pr checkout recorded for branch, or "" when there is
// none or that remote no longer exists.
func pullBaseRemote(ctx context.Context, runner Runner, dir, branch string) string {
	if branch == "" {
		return ""
	}
	base, err := runner.Output(ctx, dir, false, "config", "--get", "branch."+branch+"."+pullBaseRemoteKey)
	if err != nil || base == "" {
		return ""
	}
	names, err := runner.Output(ctx, dir, false, "remote")
	if err != nil || !slices.Contains(strings.Fields(names), base) {
		return ""
	}
	return base
}
