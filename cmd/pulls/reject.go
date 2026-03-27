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

// CmdPullsReject requests changes to a PR
var CmdPullsReject = cli.Command{
	Name:        "reject",
	Usage:       "Request changes to a pull request",
	Description: "Request changes to a pull request",
	ArgsUsage:   "<pull index> <reason>",
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		return runPullReview(ctx, gitea.ReviewStateRequestChanges, true)
	},
	Flags: flags.AllDefaultFlags,
}
