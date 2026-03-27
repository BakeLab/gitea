// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdMilestonesReopen represents a sub command of milestones to open an milestone
var CmdMilestonesReopen = cli.Command{
	Name:        "reopen",
	Aliases:     []string{"open"},
	Usage:       "Change state of one or more milestones to 'open'",
	Description: `Change state of one or more milestones to 'open'`,
	ArgsUsage:   "<milestone name> [<milestone name> ...]",
	Action: func(ctx stdctx.Context, cmd *cli.Command) error {
		return editMilestoneStatus(ctx, cmd, false)
	},
	Flags: flags.AllDefaultFlags,
}

func editMilestoneStatus(_ stdctx.Context, cmd *cli.Command, close bool) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	if ctx.Args().Len() == 0 {
		return fmt.Errorf("missing required argument: %s", ctx.Command.ArgsUsage)
	}

	state := gitea.StateOpen
	if close {
		state = gitea.StateClosed
	}

	client := ctx.Login.Client()
	repoURL := ""
	if ctx.Args().Len() > 1 {
		repoURL, err = ctx.GetRemoteRepoHTMLURL()
		if err != nil {
			return err
		}
	}
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
			fmt.Printf("%s/milestone/%d\n", repoURL, milestone.ID)
		} else {
			print.MilestoneDetails(milestone)
		}
	}
	return nil
}
