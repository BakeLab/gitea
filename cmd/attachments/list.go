// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachments

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/cmd/releases"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"github.com/urfave/cli/v3"
)

// CmdReleaseAttachmentList represents a sub command of release attachment to list release attachments
var CmdReleaseAttachmentList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List Release Attachments",
	Description: "List Release Attachments",
	ArgsUsage:   "<release-tag>", // command does not accept arguments
	Action:      RunReleaseAttachmentList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunReleaseAttachmentList list release attachments
func RunReleaseAttachmentList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	tag := ctx.Args().First()
	if len(tag) == 0 {
		return fmt.Errorf("release tag needed to list attachments")
	}

	release, err := releases.GetReleaseByTag(requestCtx, ctx.Owner, ctx.Repo, tag, client)
	if err != nil {
		return err
	}

	attachments, _, err := ctx.Login.Client().Releases.ListReleaseAttachments(requestCtx, ctx.Owner, ctx.Repo, release.ID, gitea.ListReleaseAttachmentsOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.ReleaseAttachmentsList(attachments, ctx.Output)
}
