// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package debug

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

var debug bool

// IsDebug returns true if debug mode is enabled
func IsDebug() bool {
	return debug
}

// SetDebug sets the debug mode
func SetDebug(on bool) {
	debug = on
}

// Printf prints debug information if debug mode is enabled
func Printf(info string, args ...any) {
	if debug {
		fmt.Printf("DEBUG: "+info+"\n", args...)
	}
}

// CliFlag returns the CLI flag for debug mode
func CliFlag() cli.Flag {
	return &cli.BoolFlag{
		Name:    "debug",
		Aliases: []string{"vvv"},
		Usage:   "Enable debug mode",
		Value:   false,
		Action: func(ctx context.Context, cmd *cli.Command, v bool) error {
			SetDebug(v)
			return nil
		},
	}
}
