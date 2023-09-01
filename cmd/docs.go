// Copyright 2023 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

// CmdDocs generates markdown for tea
var CmdDocs = cli.Command{
	Name:        "docs",
	Hidden:      true,
	Description: "Generate CLI docs",
	Action:      runDocs,
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "out",
			Usage:   "Path to output docs to, otherwise prints to stdout",
			Aliases: []string{"o"},
		},
	},
}

func runDocs(ctx *cli.Context) error {
	md, err := ctx.App.ToMarkdown()
	if err != nil {
		return err
	}

	outPath := ctx.String("out")
	if outPath == "" {
		fmt.Print(md)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(outPath), os.ModePerm); err != nil {
		return err
	}

	fi, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer fi.Close()
	if _, err := fi.WriteString(md); err != nil {
		return err
	}

	return nil
}
