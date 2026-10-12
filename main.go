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
	"syscall"

	"gitea.dev/cli/internal/app"
	"gitea.dev/cli/internal/clierr"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, app.System(), os.Args[1:])
	stop()
	os.Exit(code)
}

// run executes gt, reports a failure on stderr and returns the exit code. An interrupt exits
// silently, and a declined confirmation prints only "Canceled.".
func run(ctx context.Context, deps app.Dependencies, args []string) int {
	err := app.Execute(ctx, deps, args)
	if err != nil && ctx.Err() != nil {
		// Whatever failed after Ctrl-C or SIGTERM failed because of it; the user knows why.
		err = clierr.Wrap(clierr.Interrupted, err)
	}
	code := clierr.ExitCode(err)
	switch {
	case err == nil, code == clierr.Interrupted, clierr.IsQuiet(err):
	case code == clierr.Canceled && !errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintln(deps.Streams.ErrOut, "Canceled.")
	default:
		fmt.Fprintf(deps.Streams.ErrOut, "Error: %v\n", app.Explain(ctx, deps, err))
	}
	return code
}
