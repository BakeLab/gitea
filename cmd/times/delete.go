// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package times

import (
	stdctx "context"
	"fmt"
	"strconv"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdTrackedTimesDelete is a sub command of CmdTrackedTimes, and removes time from an issue
var CmdTrackedTimesDelete = cli.Command{
	Name:      "delete",
	Aliases:   []string{"rm"},
	Usage:     "Delete a single tracked time on an issue",
	UsageText: "tea times delete <issue> <time ID>",
	Action:    runTrackedTimesDelete,
	Flags:     flags.LoginRepoFlags,
}

func runTrackedTimesDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	if ctx.Args().Len() < 2 {
		return fmt.Errorf("No issue or time ID specified.\nUsage:\t%s", ctx.Command.UsageText)
	}

	issue, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	timeID, err := strconv.ParseInt(ctx.Args().Get(1), 10, 64)
	if err != nil {
		return err
	}

	_, err = client.DeleteTime(ctx.Owner, ctx.Repo, issue, timeID)
	return err
}
