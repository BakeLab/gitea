// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"fmt"
	"strings"

	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"
	"github.com/urfave/cli/v3"
)

// CmdPullsApprove approves a PR
var CmdPullsApprove = cli.Command{
	Name:        "approve",
	Aliases:     []string{"lgtm", "a"},
	Usage:       "Approve a pull request",
	Description: "Approve a pull request",
	ArgsUsage:   "<pull index> [<comment>]",
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx := context.InitCommand(cmd)
		ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

		if ctx.Args().Len() == 0 {
			return fmt.Errorf("Must specify a PR index")
		}

		idx, err := utils.ArgToIndex(ctx.Args().First())
		if err != nil {
			return err
		}

		comment := strings.Join(ctx.Args().Tail(), " ")

		return task.CreatePullReview(ctx, idx, gitea.ReviewStateApproved, comment, nil)
	},
	Flags: flags.AllDefaultFlags,
}
