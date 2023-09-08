// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v2"
)

// CmdIssuesEdit is the subcommand of issues to edit issues
var CmdIssuesEdit = cli.Command{
	Name:    "edit",
	Aliases: []string{"e"},
	Usage:   "Edit one or more issues",
	Description: `Edit one or more issues. To unset a property again,
use an empty string (eg. --milestone "").`,
	ArgsUsage: "<idx> [<idx>...]",
	Action:    runIssuesEdit,
	Flags:     flags.IssuePREditFlags,
}

func runIssuesEdit(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	if !cmd.Args().Present() {
		return fmt.Errorf("must specify at least one issue index")
	}

	opts, err := flags.GetIssuePREditFlags(ctx)
	if err != nil {
		return err
	}

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, opts.Index = range indices {
		issue, err := task.EditIssue(ctx, client, *opts)
		if err != nil {
			return err
		}
		if ctx.Args().Len() > 1 {
			fmt.Println(issue.HTMLURL)
		} else {
			print.IssueDetails(issue, nil)
		}
	}

	return nil
}
