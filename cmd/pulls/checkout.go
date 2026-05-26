// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdPullsCheckout is a command to locally checkout the given PR
var CmdPullsCheckout = cli.Command{
	Name:        "checkout",
	Aliases:     []string{"co"},
	Usage:       "Locally check out the given PR",
	Description: `Locally check out the given PR`,
	Action:      runPullsCheckout,
	ArgsUsage:   "<pull index>",
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "branch",
			Aliases: []string{"b"},
			Usage:   "Create a local branch if it doesn't exist yet",
		},
	}, flags.AllDefaultFlags...),
}

func runPullsCheckout(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{
		LocalRepo:  true,
		RemoteRepo: true,
	}); err != nil {
		return err
	}
	if ctx.Args().Len() != 1 {
		return fmt.Errorf("pull request index is required")
	}
	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	if err := task.PullCheckout(requestCtx, ctx.Login, ctx.Owner, ctx.Repo, ctx.Bool("branch"), idx, interact.PromptPassword); err != nil && !interact.IsQuitting(err) {
		return err
	}
	return nil
}
