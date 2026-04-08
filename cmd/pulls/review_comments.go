// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

var reviewCommentFieldsFlag = flags.FieldsFlag(print.PullReviewCommentFields, []string{
	"id", "path", "line", "body", "reviewer", "resolver",
})

// CmdPullsReviewComments lists review comments on a pull request
var CmdPullsReviewComments = cli.Command{
	Name:        "review-comments",
	Aliases:     []string{"rc"},
	Usage:       "List review comments on a pull request",
	Description: "List review comments on a pull request",
	ArgsUsage:   "<pull index>",
	Action:      runPullsReviewComments,
	Flags:       append([]cli.Flag{reviewCommentFieldsFlag}, flags.AllDefaultFlags...),
}

func runPullsReviewComments(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() < 1 {
		return fmt.Errorf("pull request index is required")
	}

	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	comments, err := task.ListPullReviewComments(ctx, idx)
	if err != nil {
		return err
	}

	fields, err := reviewCommentFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	return print.PullReviewCommentsList(comments, ctx.Output, fields)
}
