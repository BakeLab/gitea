// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"io"

	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/terminal"
)

func (a *application) git(ctx context.Context, args ...string) (string, error) {
	return a.gitIn(ctx, a.options.cwd, args...)
}

// gitIn runs Git in dir, which is relative to the process's working directory, not to --cwd.
func (a *application) gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	snapshot, err := a.settings()
	if err != nil {
		return "", err
	}
	return a.deps.Git.Output(ctx, dir, a.policy(snapshot).CanPrompt(a.deps.Streams), args...)
}

// heldGit returns a runner that holds back Git's diagnostics, and a function that shows them.
func (a *application) heldGit() (git.Runner, func()) {
	runner, ok := a.deps.Git.(git.System)
	if !ok || runner.ErrOut == nil {
		return a.deps.Git, func() {}
	}
	shown, held := runner.ErrOut, &bytes.Buffer{}
	runner.ErrOut = held
	return runner, func() {
		_, _ = io.Copy(terminal.SafeWriter(shown, a.deps.Streams.ErrTTY, false), held)
	}
}

// currentBranch names the checked-out branch without writing Git diagnostics.
func (a *application) currentBranch(ctx context.Context) (string, error) {
	return git.CurrentBranch(ctx, a.gitProbe(), a.options.cwd)
}

// gitProbe runs exploratory Git commands whose failures are expected, without Git's diagnostics.
func (a *application) gitProbe() git.Runner {
	if runner, ok := a.deps.Git.(git.System); ok {
		runner.ErrOut = io.Discard
		return runner
	}
	return a.deps.Git
}

// insideClone reports whether the working directory is inside a Git work tree.
func (a *application) insideClone(ctx context.Context) bool {
	inside, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree")
	return inside == "true"
}
