// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"

	"gitea.dev/tea/internal/git"
)

func (a *application) git(ctx context.Context, args ...string) (string, error) {
	return a.gitIn(ctx, a.options.cwd, args...)
}

// gitIn runs Git in dir, which is relative to the process's working directory, not to --cwd.
func (a *application) gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	snapshot, err := a.deps.Settings.Load()
	if err != nil {
		return "", err
	}
	return a.deps.Git.Output(ctx, dir, a.policy(snapshot).CanPrompt(a.deps.Streams), args...)
}

// currentBranch names the checked-out branch without writing Git diagnostics.
func (a *application) currentBranch(ctx context.Context) (string, error) {
	return a.gitProbe().Output(ctx, a.options.cwd, false, "symbolic-ref", "--short", "HEAD")
}

// gitProbe runs exploratory Git commands whose failures are expected, without Git's diagnostics.
func (a *application) gitProbe() git.Runner {
	if runner, ok := a.deps.Git.(git.System); ok {
		runner.ErrOut = io.Discard
		return runner
	}
	return a.deps.Git
}
