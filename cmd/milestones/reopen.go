// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdMilestonesReopen represents a sub command of milestones to open an milestone
var CmdMilestonesReopen = cli.Command{
	Name:        "reopen",
	Aliases:     []string{"open"},
	Usage:       "Change state of one or more milestones to 'open'",
	Description: `Change state of one or more milestones to 'open'`,
	ArgsUsage:   "<milestone name> [<milestone name> ...]",
	Action: func(ctx *cli.Context) error {
		return editMilestoneStatus(ctx, false)
	},
	Flags: flags.AllDefaultFlags,
}

func editMilestoneStatus(cmd *cli.Context, close bool) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	if ctx.Args().Len() == 0 {
		return fmt.Errorf(ctx.Command.ArgsUsage)
	}

	state := gitea.StateOpen
	if close {
		state = gitea.StateClosed
	}

	client := ctx.Login.Client()
	for _, ms := range ctx.Args().Slice() {
		opts := gitea.EditMilestoneOption{
			State: &state,
			Title: ms,
		}
		milestone, _, err := client.EditMilestoneByName(ctx.Owner, ctx.Repo, ms, opts)
		if err != nil {
			return err
		}

		if ctx.Args().Len() > 1 {
			fmt.Printf("%s/milestone/%d\n", ctx.GetRemoteRepoHTMLURL(), milestone.ID)
		} else {
			print.MilestoneDetails(milestone)
		}
	}
	return nil
}
