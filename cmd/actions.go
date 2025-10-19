// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/actions"

	"github.com/urfave/cli/v3"
)

// CmdActions represents the actions command for managing Gitea Actions
var CmdActions = cli.Command{
	Name:        "actions",
	Aliases:     []string{"action"},
	Category:    catEntities,
	Usage:       "Manage repository actions",
	Description: "Manage repository actions including secrets, variables, and workflows",
	Action:      runActionsDefault,
	Commands: []*cli.Command{
		&actions.CmdActionsSecrets,
		&actions.CmdActionsVariables,
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "repo",
			Usage: "repository to operate on",
		},
		&cli.StringFlag{
			Name:  "login",
			Usage: "gitea login instance to use",
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "output format [table, csv, simple, tsv, yaml, json]",
		},
	},
}

func runActionsDefault(ctx stdctx.Context, cmd *cli.Command) error {
	// Default to showing help
	return cli.ShowCommandHelp(ctx, cmd, "actions")
}
