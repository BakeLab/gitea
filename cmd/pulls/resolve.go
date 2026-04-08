// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"

	"github.com/urfave/cli/v3"
)

// CmdPullsResolve resolves a review comment on a pull request
var CmdPullsResolve = cli.Command{
	Name:        "resolve",
	Usage:       "Resolve a review comment on a pull request",
	Description: "Resolve a review comment on a pull request",
	ArgsUsage:   "<comment id>",
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		return runResolveComment(ctx, task.ResolvePullReviewComment)
	},
	Flags: flags.AllDefaultFlags,
}
