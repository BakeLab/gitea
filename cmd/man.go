// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	docs "github.com/urfave/cli-docs/v3"
	"github.com/urfave/cli/v3"
)

// DocRenderFlags are the flags for documentation generation, used by `./docs/docs.go` and the `generate-man-page` sub command
var DocRenderFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "out",
		Usage:   "Path to output docs to, otherwise prints to stdout",
		Aliases: []string{"o"},
	},
}

// CmdGenerateManPage is the sub command to generate the `tea` man page
var CmdGenerateManPage = cli.Command{
	Name:   "man",
	Usage:  "Generate man page",
	Hidden: true,
	Flags:  DocRenderFlags,
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return RenderDocs(cmd, cmd.Root(), docs.ToMan)
	},
}

// RenderDocs renders the documentation for `target` using the supplied `render` function
func RenderDocs(cmd, target *cli.Command, render func(*cli.Command) (string, error)) error {
	out, err := render(target)
	if err != nil {
		return err
	}
	outPath := cmd.String("out")
	if outPath == "" {
		fmt.Print(out)
		return nil
	}

	if err = os.MkdirAll(filepath.Dir(outPath), os.ModePerm); err != nil {
		return err
	}

	fi, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer fi.Close()
	if _, err = fi.WriteString(out); err != nil {
		return err
	}

	return nil
}
