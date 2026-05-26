// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	"github.com/urfave/cli/v3"

	"gitea.dev/tea/cmd/milestones"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
)

// CmdMilestones represents to operate repositories milestones.
var CmdMilestones = cli.Command{
	Name:        "milestones",
	Aliases:     []string{"milestone", "ms"},
	Category:    catEntities,
	Usage:       "List and create milestones",
	Description: `List and create milestones`,
	ArgsUsage:   "[<milestone name>]",
	Action:      runMilestones,
	Commands: []*cli.Command{
		&milestones.CmdMilestonesList,
		&milestones.CmdMilestonesCreate,
		&milestones.CmdMilestonesClose,
		&milestones.CmdMilestonesDelete,
		&milestones.CmdMilestonesReopen,
		&milestones.CmdMilestonesIssues,
	},
	Flags: milestones.CmdMilestonesList.Flags,
}

func runMilestones(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runMilestoneDetail(ctx, cmd, cmd.Args().First())
	}
	return milestones.RunMilestonesList(ctx, cmd)
}

func runMilestoneDetail(requestCtx stdctx.Context, cmd *cli.Command, name string) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	milestone, _, err := client.Repositories.GetMilestoneByName(requestCtx, ctx.Owner, ctx.Repo, name)
	if err != nil {
		return err
	}

	print.MilestoneDetails(milestone)
	return nil
}
