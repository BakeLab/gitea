// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/task"
	"github.com/urfave/cli/v3"
)

// CmdPullsCreate creates a pull request
var CmdPullsCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create a pull-request",
	Description: "Create a pull-request in the current repo",
	Action:      runPullsCreate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "head",
			Usage: "Branch name of the PR source (default is current one). To specify a different head repo, use <user>:<branch>",
		},
		&cli.StringFlag{
			Name:    "base",
			Aliases: []string{"b"},
			Usage:   "Branch name of the PR target (default is repos default branch)",
		},
		&cli.BoolFlag{
			Name:    "allow-maintainer-edits",
			Aliases: []string{"edits"},
			Usage:   "Enable maintainers to push to the base branch of created pull",
			Value:   true,
		},
	}, flags.IssuePRCreateFlags...),
}

func runPullsCreate(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)

	// no args -> interactive mode
	if ctx.NumFlags() == 0 {
		if err := interact.CreatePull(ctx); err != nil && !interact.IsQuitting(err) {
			return err
		}
		return nil
	}

	// else use args to create PR
	opts, err := flags.GetIssuePRCreateFlags(ctx)
	if err != nil {
		return err
	}

	return task.CreatePull(
		ctx,
		ctx.String("base"),
		ctx.String("head"),
		ctx.Bool("allow-maintainer-edits"),
		opts,
	)
}
