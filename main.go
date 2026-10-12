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
	defer stop()
	deps := app.System()
	err := app.Execute(ctx, deps, os.Args[1:])
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		err = clierr.Wrap(clierr.Interrupted, err)
	}
	if err != nil {
		fmt.Fprintf(deps.Streams.ErrOut, "Error: %v\n", err)
	}
	os.Exit(clierr.ExitCode(err))
}
