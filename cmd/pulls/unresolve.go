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

// CmdPullsUnresolve unresolves a review comment on a pull request
var CmdPullsUnresolve = cli.Command{
	Name:        "unresolve",
	Usage:       "Unresolve a review comment on a pull request",
	Description: "Unresolve a review comment on a pull request",
	ArgsUsage:   "<comment id>",
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		return runResolveComment(ctx, task.UnresolvePullReviewComment)
	},
	Flags: flags.AllDefaultFlags,
}
