// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdIssuesClose represents a sub command of issues to close an issue
var CmdIssuesClose = cli.Command{
	Name:        "close",
	Usage:       "Change state of one ore more issues to 'closed'",
	Description: `Change state of one ore more issues to 'closed'`,
	ArgsUsage:   "<issue index> [<issue index>...]",
	Action: func(ctx *cli.Context) error {
		var s = gitea.StateClosed
		return editIssueState(ctx, gitea.EditIssueOption{State: &s})
	},
	Flags: flags.AllDefaultFlags,
}

// editIssueState abstracts the arg parsing to edit the given issue
func editIssueState(cmd *cli.Context, opts gitea.EditIssueOption) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	if ctx.Args().Len() == 0 {
		return fmt.Errorf(ctx.Command.ArgsUsage)
	}

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, index := range indices {
		issue, _, err := client.EditIssue(ctx.Owner, ctx.Repo, index, opts)
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
