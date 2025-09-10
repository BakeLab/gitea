// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Tea is command line tool for Gitea.
package main // import "code.gitea.io/tea"

import (
	"context"
	"fmt"
	"os"

	"code.gitea.io/tea/cmd"
	"code.gitea.io/tea/modules/debug"
)

func main() {
	app := cmd.App()
	app.Flags = append(app.Flags, debug.CliFlag())
	err := app.Run(context.Background(), os.Args)
	if err != nil {
		// app.Run already exits for errors implementing ErrorCoder,
		// so we only handle generic errors with code 1 here.
		fmt.Fprintf(app.ErrWriter, "Error: %v\n", err)
		os.Exit(1)
	}
}
