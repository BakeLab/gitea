// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/task"

	"github.com/urfave/cli/v3"
)

// CmdIssuesCreate represents a sub command of issues to create issue
var CmdIssuesCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create an issue on repository",
	Description: `Create an issue on repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runIssuesCreate,
	Flags:       flags.IssuePRCreateFlags,
}

func runIssuesCreate(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	if ctx.NumFlags() == 0 {
		err := interact.CreateIssue(ctx.Login, ctx.Owner, ctx.Repo)
		if err != nil && !interact.IsQuitting(err) {
			return err
		}
		return nil
	}

	opts, err := flags.GetIssuePRCreateFlags(ctx)
	if err != nil {
		return err
	}

	return task.CreateIssue(
		ctx.Login,
		ctx.Owner,
		ctx.Repo,
		*opts,
	)
}
