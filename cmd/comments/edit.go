// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/theme"
	"gitea.dev/tea/modules/utils"

	"charm.land/huh/v2"
	"github.com/urfave/cli/v3"
)

// CmdCommentsEdit edits an existing comment.
var CmdCommentsEdit = cli.Command{
	Name:    "edit",
	Aliases: []string{"e"},
	Usage:   "Edit the body of an existing comment",
	Description: `Edit the body of an existing comment by its comment ID. Use 'tea comments list <issue>' to find IDs.

The new body can be supplied as a positional argument, piped on stdin, or (if neither is given and stdin is a terminal) entered in your $EDITOR.`,
	ArgsUsage: "<comment id> [<new body>]",
	Action:    RunCommentsEdit,
	Flags:     flags.AllDefaultFlags,
}

// RunCommentsEdit updates the body of an existing comment.
func RunCommentsEdit(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() == 0 {
		return fmt.Errorf("please specify comment id")
	}

	id, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return fmt.Errorf("invalid comment id %q: %s", ctx.Args().First(), err)
	}

	body := strings.Join(ctx.Args().Tail(), " ")
	if len(body) == 0 && interact.IsStdinPiped() {
		if bodyStdin, err := io.ReadAll(ctx.Reader); err != nil {
			return err
		} else if len(bodyStdin) != 0 {
			body = string(bodyStdin)
		}
	} else if len(body) == 0 {
		// Fetch current body to pre-populate the editor.
		client := ctx.Login.Client()
		current, _, fetchErr := client.Issues.GetIssueComment(requestCtx, ctx.Owner, ctx.Repo, id)
		if fetchErr != nil {
			return fmt.Errorf("could not fetch comment %d: %s", id, fetchErr)
		}
		body = current.Body
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title("Comment(markdown):").
					ExternalEditor(config.GetPreferences().Editor).
					EditorExtension("md").
					Value(&body),
			),
		).WithTheme(theme.GetTheme()).
			Run(); err != nil {
			return err
		}
	}

	if len(body) == 0 {
		return errors.New("no comment content provided")
	}

	client := ctx.Login.Client()
	comment, _, err := client.Issues.EditIssueComment(requestCtx, ctx.Owner, ctx.Repo, id, gitea.EditIssueCommentOption{
		Body: body,
	})
	if err != nil {
		return err
	}

	print.Comment(comment)
	return nil
}
