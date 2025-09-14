// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:generates
package main

import (
	"context"
	"os"

	"code.gitea.io/tea/cmd"
	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

// CmdDocs generates markdown for tea
func main() {
	cli := &cli.Command{
		Name:        "docs",
		Hidden:      true,
		Description: "Generate CLI docs",
		Flags:       cmd.DocRenderFlags,
		Action: func(ctx context.Context, params *cli.Command) error {
			return cmd.RenderDocs(params, cmd.App(), docs.ToMarkdown)
		},
	}
	cli.Run(context.Background(), os.Args)
}
