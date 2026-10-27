// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// gitea-cli is a scriptable command line tool for Gitea.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gitea.dev/cli/internal/app"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Restore the default handling once the first signal arrived, so that a second Ctrl-C or SIGTERM
	// ends gt even while it blocks on something that ignores ctx, such as reading stdin.
	go func() {
		<-ctx.Done()
		stop()
	}()
	deps := app.System()
	execute := app.Execute
	// Extensions installed with --compat-gh call gt through a gh link.
	if strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe") == "gh" {
		execute = app.ExecuteGH
	}
	code := run(ctx, deps, os.Args[1:], execute)
	stop()
	deps.Streams.Restore()
	os.Exit(code)
}

// run executes gt, reports a failure on stderr and returns the exit code. An interrupt exits
// silently, and a declined confirmation prints only "Canceled.".
func run(ctx context.Context, deps app.Dependencies, args []string, execute func(context.Context, app.Dependencies, []string) error) int {
	err := execute(ctx, deps, args)
	// An extension that handled Ctrl-C reported its outcome with its own exit code.
	if err != nil && ctx.Err() != nil && !clierr.IsQuiet(err) {
		// Whatever failed after Ctrl-C or SIGTERM failed because of it; the user knows why.
		err = clierr.Wrap(clierr.Interrupted, err)
	}
	code := clierr.ExitCode(err)
	switch {
	case err == nil, clierr.IsQuiet(err):
	case code == clierr.Interrupted:
		reportDraft(deps, err)
	case code == clierr.Canceled && !errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(deps.Streams.ErrOut, "Canceled.")
		reportDraft(deps, err)
	default:
		// Errors quote names the server supplied, which may carry terminal escapes.
		fmt.Fprintf(deps.Streams.ErrOut, "Error: %s\n", terminal.SafeText(app.Explain(ctx, deps, err).Error()))
	}
	return code
}

// reportDraft tells where the text of a canceled command was saved; the error is otherwise not shown.
func reportDraft(deps app.Dependencies, err error) {
	if path := app.DraftPath(err); path != "" {
		fmt.Fprintf(deps.Streams.ErrOut, "Draft saved at %s\n", path)
	}
}
