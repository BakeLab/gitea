// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/theme"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"
)

// CmdAddComment is the main command to operate with notifications
var CmdAddComment = cli.Command{
	Name:        "comment",
	Aliases:     []string{"c"},
	Category:    catEntities,
	Usage:       "Add a comment to an issue / pr",
	Description: "Add a comment to an issue / pr",
	ArgsUsage:   "<issue / pr index> [<comment body>]",
	Action:      runAddComment,
	Flags:       flags.AllDefaultFlags,
}

func runAddComment(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	args := ctx.Args()
	if args.Len() == 0 {
		return fmt.Errorf("Please specify issue / pr index")
	}

	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	body := strings.Join(ctx.Args().Tail(), " ")
	if interact.IsStdinPiped() {
		// custom solution until https://github.com/AlecAivazis/survey/issues/328 is fixed
		if bodyStdin, err := io.ReadAll(ctx.Reader); err != nil {
			return err
		} else if len(bodyStdin) != 0 {
			body = strings.Join([]string{body, string(bodyStdin)}, "\n\n")
		}
	} else if len(body) == 0 {
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
	comment, _, err := client.CreateIssueComment(ctx.Owner, ctx.Repo, idx, gitea.CreateIssueCommentOption{
		Body: body,
	})
	if err != nil {
		return err
	}

	print.Comment(comment)

	return nil
}
