// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
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
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		return runPullReview(ctx, gitea.ReviewStateApproved, false)
	},
	Flags: flags.AllDefaultFlags,
}
