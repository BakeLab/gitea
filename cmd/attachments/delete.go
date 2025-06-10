// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachments

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
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
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	client := ctx.Login.Client()

	if ctx.Args().Len() < 2 {
		return fmt.Errorf("No release tag or attachment names specified.\nUsage:\t%s", ctx.Command.UsageText)
	}

	tag := ctx.Args().First()
	if len(tag) == 0 {
		return fmt.Errorf("Release tag needed to delete attachment")
	}

	if !ctx.Bool("confirm") {
		fmt.Println("Are you sure? Please confirm with -y or --confirm.")
		return nil
	}

	release, err := getReleaseByTag(ctx.Owner, ctx.Repo, tag, client)
	if err != nil {
		return err
	}

	existing, _, err := client.ListReleaseAttachments(ctx.Owner, ctx.Repo, release.ID, gitea.ListReleaseAttachmentsOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		return err
	}

	for _, name := range ctx.Args().Slice()[1:] {
		var attachment *gitea.Attachment
		for _, a := range existing {
			if a.Name == name {
				attachment = a
			}
		}
		if attachment == nil {
			return fmt.Errorf("Release does not have attachment named '%s'", name)
		}

		_, err = client.DeleteReleaseAttachment(ctx.Owner, ctx.Repo, release.ID, attachment.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

func getReleaseAttachmentByName(owner, repo string, release int64, name string, client *gitea.Client) (*gitea.Attachment, error) {
	al, _, err := client.ListReleaseAttachments(owner, repo, release, gitea.ListReleaseAttachmentsOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		return nil, err
	}
	if len(al) == 0 {
		return nil, fmt.Errorf("Release does not have any attachments")
	}
	for _, a := range al {
		if a.Name == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("Attachment does not exist")
}
