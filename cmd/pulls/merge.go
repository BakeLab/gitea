// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	gitea "gitea.dev/sdk"
	"github.com/urfave/cli/v3"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"
)

// CmdPullsMerge merges a PR
var CmdPullsMerge = cli.Command{
	Name:        "merge",
	Aliases:     []string{"m"},
	Usage:       "Merge a pull request",
	Description: "Merge a pull request",
	ArgsUsage:   "<pull index>",
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "style",
			Aliases: []string{"s"},
			Usage:   "Kind of merge to perform: merge, rebase, squash, rebase-merge",
			Value:   "merge",
		},
		&cli.StringFlag{
			Name:    "title",
			Aliases: []string{"t"},
			Usage:   "Merge commit title",
		},
		&cli.StringFlag{
			Name:    "message",
			Aliases: []string{"m"},
			Usage:   "Merge commit message",
		},
	}, flags.AllDefaultFlags...),
	Action: func(requestCtx stdctx.Context, cmd *cli.Command) error {
		ctx, err := context.InitCommand(cmd)
		if err != nil {
			return err
		}
		if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
			return err
		}

		if ctx.Args().Len() != 1 {
			// If no PR index is provided, try interactive mode
			if err := interact.MergePull(requestCtx, ctx); err != nil && !interact.IsQuitting(err) {
				return err
			}
			return nil
		}

		idx, err := utils.ArgToIndex(ctx.Args().First())
		if err != nil {
			return err
		}

		return task.PullMerge(requestCtx, ctx.Login, ctx.Owner, ctx.Repo, idx, gitea.MergePullRequestOption{
			Style:   gitea.MergeStyle(ctx.String("style")),
			Title:   ctx.String("title"),
			Message: ctx.String("message"),
		})
	},
}
