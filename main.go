// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Tea is command line tool for Gitea.
package main // import "code.gitea.io/tea"

import (
	"context"
	"errors"
	"fmt"
	"os"

	"code.gitea.io/tea/cmd"
	teacontext "code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/debug"
)

func main() {
	app := cmd.App()
	app.Flags = append(app.Flags, debug.CliFlag())
	err := app.Run(context.Background(), preprocessArgs(os.Args))
	if err != nil {
		if errors.Is(err, teacontext.ErrCommandCanceled) {
			os.Exit(0)
		}
		// app.Run already exits for errors implementing ErrorCoder,
		// so we only handle generic errors with code 1 here.
		fmt.Fprintf(app.ErrWriter, "Error: %v\n", err)
		os.Exit(1)
	}
}

// preprocessArgs normalizes command-line arguments.
// Converts "-o-" to "-o -" for the api command's output flag.
func preprocessArgs(args []string) []string {
	result := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if arg == "-o-" {
			result = append(result, "-o", "-")
		} else {
			result = append(result, arg)
		}
	}
	return result
}
