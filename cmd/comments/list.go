// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdCommentsList lists comments on an issue or pull request.
var CmdCommentsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List comments on an issue or pull request",
	Description: "List comments on an issue or pull request. Comment IDs returned here are the IDs accepted by 'tea comments edit' and 'tea comments delete'.",
	ArgsUsage:   "<issue / pr index>",
	Action:      RunCommentsList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunCommentsList lists comments on the given issue/PR.
func RunCommentsList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() == 0 {
		return fmt.Errorf("please specify issue / pr index")
	}

	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	comments, _, err := client.Issues.ListIssueComments(requestCtx, ctx.Owner, ctx.Repo, idx, gitea.ListIssueCommentOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.CommentsList(comments, ctx.Output)
}
