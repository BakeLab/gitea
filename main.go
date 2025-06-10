// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Tea is command line tool for Gitea.
package main // import "code.gitea.io/tea"

import (
	"context"
	"fmt"
	"os"

	"code.gitea.io/tea/cmd"
)

func main() {
	app := cmd.App()
	err := app.Run(context.Background(), os.Args)
	if err != nil {
		// app.Run already exits for errors implementing ErrorCoder,
		// so we only handle generic errors with code 1 here.
		fmt.Fprintf(app.ErrWriter, "Error: %v\n", err)
		os.Exit(1)
	}
}
