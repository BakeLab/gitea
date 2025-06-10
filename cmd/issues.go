// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/issues"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdIssues represents to login a gitea server.
var CmdIssues = cli.Command{
	Name:        "issues",
	Aliases:     []string{"issue", "i"},
	Category:    catEntities,
	Usage:       "List, create and update issues",
	Description: `Lists issues when called without argument. If issue index is provided, will show it in detail.`,
	ArgsUsage:   "[<issue index>]",
	Action:      runIssues,
	Commands: []*cli.Command{
		&issues.CmdIssuesList,
		&issues.CmdIssuesCreate,
		&issues.CmdIssuesEdit,
		&issues.CmdIssuesReopen,
		&issues.CmdIssuesClose,
	},
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:  "comments",
			Usage: "Whether to display comments (will prompt if not provided & run interactively)",
		},
	}, issues.CmdIssuesList.Flags...),
}

func runIssues(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runIssueDetail(ctx, cmd, cmd.Args().First())
	}
	return issues.RunIssuesList(ctx, cmd)
}

func runIssueDetail(_ stdctx.Context, cmd *cli.Command, index string) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	idx, err := utils.ArgToIndex(index)
	if err != nil {
		return err
	}
	client := ctx.Login.Client()
	issue, _, err := client.GetIssue(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return err
	}
	reactions, _, err := client.GetIssueReactions(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return err
	}
	print.IssueDetails(issue, reactions)

	if issue.Comments > 0 {
		err = interact.ShowCommentsMaybeInteractive(ctx, idx, issue.Comments)
		if err != nil {
			return fmt.Errorf("error loading comments: %v", err)
		}
	}

	return nil
}
