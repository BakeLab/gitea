// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import (
	stdctx "context"
	"fmt"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdCommentsDelete deletes one or more comments by ID.
var CmdCommentsDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete one or more comments by ID",
	Description: "Delete one or more comments by their comment ID. Use 'tea comments list <issue>' to find IDs.",
	ArgsUsage:   "<comment id> [<comment id>...]",
	Action:      RunCommentsDelete,
	Flags:       flags.AllDefaultFlags,
}

// RunCommentsDelete removes one or more comments.
func RunCommentsDelete(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() == 0 {
		return fmt.Errorf("please specify at least one comment id")
	}

	ids, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return fmt.Errorf("invalid comment id: %s", err)
	}

	client := ctx.Login.Client()
	for _, id := range ids {
		if _, err := client.Issues.DeleteIssueComment(requestCtx, ctx.Owner, ctx.Repo, id); err != nil {
			return fmt.Errorf("could not delete comment %d: %s", id, err)
		}
		fmt.Printf("Deleted comment %d\n", id)
	}
	return nil
}
