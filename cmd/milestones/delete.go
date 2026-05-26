// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	stdctx "context"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdMilestonesDelete represents a sub command of milestones to delete an milestone
var CmdMilestonesDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "delete a milestone",
	Description: "delete a milestone",
	ArgsUsage:   "<milestone name>",
	Action:      deleteMilestone,
	Flags:       flags.AllDefaultFlags,
}

func deleteMilestone(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	_, err = client.Repositories.DeleteMilestoneByName(requestCtx, ctx.Owner, ctx.Repo, ctx.Args().First())
	return err
}
