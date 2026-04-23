// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachments

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/cmd/releases"
	"code.gitea.io/tea/modules/context"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdReleaseAttachmentDelete represents a sub command of Release Attachments to delete a release attachment
var CmdReleaseAttachmentDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete one or more release attachments",
	Description: `Delete one or more release attachments`,
	ArgsUsage:   "<release tag> <attachment name> [<attachment name>...]",
	Action:      runReleaseAttachmentDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "Confirm deletion (required)",
		},
	}, flags.AllDefaultFlags...),
}

func runReleaseAttachmentDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	if ctx.Args().Len() < 2 {
		return fmt.Errorf("no release tag or attachment names specified.\nUsage:\t%s", ctx.Command.UsageText)
	}

	tag := ctx.Args().First()
	if len(tag) == 0 {
		return fmt.Errorf("release tag needed to delete attachment")
	}

	if !ctx.Bool("confirm") {
		fmt.Println("Are you sure? Please confirm with -y or --confirm.")
		return nil
	}

	release, err := releases.GetReleaseByTag(ctx.Owner, ctx.Repo, tag, client)
	if err != nil {
		return err
	}

	var existing []*gitea.Attachment
	for page := 1; ; {
		page_attachments, resp, err := client.ListReleaseAttachments(ctx.Owner, ctx.Repo, release.ID, gitea.ListReleaseAttachmentsOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return err
		}
		existing = append(existing, page_attachments...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	for _, name := range ctx.Args().Slice()[1:] {
		var attachment *gitea.Attachment
		for _, a := range existing {
			if a.Name == name {
				attachment = a
			}
		}
		if attachment == nil {
			return fmt.Errorf("release does not have attachment named '%s'", name)
		}

		_, err = client.DeleteReleaseAttachment(ctx.Owner, ctx.Repo, release.ID, attachment.ID)
		if err != nil {
			return err
		}
	}

	return nil
}
