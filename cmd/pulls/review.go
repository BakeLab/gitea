// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"
	"os"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdPullsReview starts an interactive review session
var CmdPullsReview = cli.Command{
	Name:        "review",
	Usage:       "Interactively review a pull request",
	Description: "Interactively review a pull request",
	ArgsUsage:   "<pull index>",
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
			return err
		}

		if !ctx.Args().Present() {
			return fmt.Errorf("must specify at least one PR index")
		}

		// This command is intentionally interactive. Fail early in CI / non-TTY
		// contexts rather than hanging on prompts.
		if os.Getenv("CI") != "" || !print.IsInteractive() || interact.IsStdinPiped() {
			return fmt.Errorf("pull review requires an interactive terminal")
		}

		for _, arg := range ctx.Args().Slice() {
			idx, err := utils.ArgToIndex(arg)
			if err != nil {
				return err
			}

			if err := interact.ReviewPull(ctx, idx); err != nil && !interact.IsQuitting(err) {
				return err
			}
		}

		return nil
	},
	Flags: flags.AllDefaultFlags,
}
