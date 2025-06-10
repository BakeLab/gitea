// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:generates
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
		Action: func(ctx context.Context, c *cli.Command) error {

			md, err := docs.ToMarkdown(cmd.App())
			if err != nil {
				return err
			}
			outPath := c.String("out")
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

		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "out",
				Usage:   "Path to output docs to, otherwise prints to stdout",
				Aliases: []string{"o"},
			},
		},
	}
	cli.Run(context.Background(), os.Args)
}
