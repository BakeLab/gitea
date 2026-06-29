// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package comments

import (
	stdctx "context"
	"errors"
	"fmt"
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

// CmdCommentsAdd adds a comment to an issue or pull request.
var CmdCommentsAdd = cli.Command{
	Name:        "add",
	Aliases:     []string{"a"},
	Usage:       "Add a comment to an issue or pull request",
	Description: "Add a comment to an issue or pull request.",
	ArgsUsage:   "<issue / pr index> [<comment body>]",
	Action:      RunCommentsAdd,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "description",
			Aliases: []string{"d"},
			Usage:   "comment body (alternative to the positional argument)",
		},
	}, flags.AllDefaultFlags...),
}

// RunCommentsAdd creates a new comment.
func RunCommentsAdd(requestCtx stdctx.Context, cmd *cli.Command) error {
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

	stdinPiped := interact.IsStdinPiped()
	body, err := resolveBody(strings.Join(ctx.Args().Tail(), " "), ctx.String("description"), stdinPiped, ctx.Reader)
	if err != nil {
		return err
	}
	if len(body) == 0 && !stdinPiped {
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
	comment, _, err := client.Issues.CreateIssueComment(requestCtx, ctx.Owner, ctx.Repo, idx, gitea.CreateIssueCommentOption{
		Body: body,
	})
	if err != nil {
		return err
	}

	print.Comment(comment)
	return nil
}
