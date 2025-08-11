// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"
	"github.com/urfave/cli/v3"
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
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		ctx := context.InitCommand(cmd)
		ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

		if ctx.Args().Len() != 1 {
			// If no PR index is provided, try interactive mode
			if err := interact.MergePull(ctx); err != nil && !interact.IsQuitting(err) {
				return err
			}
			return nil
		}

		idx, err := utils.ArgToIndex(ctx.Args().First())
		if err != nil {
			return err
		}

		return task.PullMerge(ctx.Login, ctx.Owner, ctx.Repo, idx, gitea.MergePullRequestOption{
			Style:   gitea.MergeStyle(ctx.String("style")),
			Title:   ctx.String("title"),
			Message: ctx.String("message"),
		})
	},
}
