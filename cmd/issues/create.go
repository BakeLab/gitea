// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	stdctx "context"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"

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

func runIssuesCreate(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.IsInteractiveMode() {
		err := interact.CreateIssue(requestCtx, ctx.Login, ctx.Owner, ctx.Repo)
		if err != nil && !interact.IsQuitting(err) {
			return err
		}
		return nil
	}

	opts, err := flags.GetIssuePRCreateFlags(requestCtx, ctx)
	if err != nil {
		return err
	}

	return task.CreateIssue(requestCtx, ctx.Login,
		ctx.Owner,
		ctx.Repo,
		*opts,
	)
}
