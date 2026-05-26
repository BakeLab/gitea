// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/utils"
	"github.com/urfave/cli/v3"
)

// CmdIssuesClose represents a sub command of issues to close an issue
var CmdIssuesClose = cli.Command{
	Name:        "close",
	Usage:       "Change state of one ore more issues to 'closed'",
	Description: `Change state of one ore more issues to 'closed'`,
	ArgsUsage:   "<issue index> [<issue index>...]",
	Action: func(ctx stdctx.Context, cmd *cli.Command) error {
		s := gitea.StateClosed
		return editIssueState(ctx, cmd, gitea.EditIssueOption{State: &s})
	},
	Flags: flags.AllDefaultFlags,
}

// editIssueState abstracts the arg parsing to edit the given issue
func editIssueState(requestCtx stdctx.Context, cmd *cli.Command, opts gitea.EditIssueOption) error {
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

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, index := range indices {
		issue, _, err := client.Issues.EditIssue(requestCtx, ctx.Owner, ctx.Repo, index, opts)
		if err != nil {
			return err
		}

		if len(indices) > 1 {
			fmt.Println(issue.HTMLURL)
		} else {
			print.IssueDetails(issue, nil)
		}
	}
	return nil
}
