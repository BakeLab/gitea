// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package times

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdTrackedTimesReset is a subcommand of CmdTrackedTimes, and
// clears all tracked times on an issue.
var CmdTrackedTimesReset = cli.Command{
	Name:      "reset",
	Usage:     "Reset tracked time on an issue",
	UsageText: "tea times reset <issue>",
	Action:    runTrackedTimesReset,
	Flags:     flags.LoginRepoFlags,
}

func runTrackedTimesReset(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	if ctx.Args().Len() != 1 {
		return fmt.Errorf("no issue specified.\nUsage:\t%s", ctx.Command.UsageText)
	}

	issue, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	_, err = client.ResetIssueTime(ctx.Owner, ctx.Repo, issue)
	return err
}
