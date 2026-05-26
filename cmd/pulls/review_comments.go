// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"

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

func runPullsReviewComments(requestCtx stdctx.Context, cmd *cli.Command) error {
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

	comments, err := task.ListPullReviewComments(requestCtx, ctx, idx)
	if err != nil {
		return err
	}

	fields, err := reviewCommentFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	return print.PullReviewCommentsList(comments, ctx.Output, fields)
}
