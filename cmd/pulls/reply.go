// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdPullsReply replies to a review comment on a pull request.
var CmdPullsReply = cli.Command{
	Name:        "reply",
	Usage:       "Reply to a pull request review comment",
	Description: "Reply to a pull request review comment",
	ArgsUsage:   "<pull index> <comment id> [<reply>]",
	Action: func(requestCtx stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		return runPullReviewReply(requestCtx, ctx)
	},
	Flags: flags.AllDefaultFlags,
}
