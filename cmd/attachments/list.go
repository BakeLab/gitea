// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachments

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
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
func RunReleaseAttachmentList(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	client := ctx.Login.Client()

	tag := ctx.Args().First()
	if len(tag) == 0 {
		return fmt.Errorf("Release tag needed to list attachments")
	}

	release, err := getReleaseByTag(ctx.Owner, ctx.Repo, tag, client)
	if err != nil {
		return err
	}

	attachments, _, err := ctx.Login.Client().ListReleaseAttachments(ctx.Owner, ctx.Repo, release.ID, gitea.ListReleaseAttachmentsOptions{
		ListOptions: flags.GetListOptions(),
	})
	if err != nil {
		return err
	}

	print.ReleaseAttachmentsList(attachments, ctx.Output)
	return nil
}

func getReleaseByTag(owner, repo, tag string, client *gitea.Client) (*gitea.Release, error) {
	rl, _, err := client.ListReleases(owner, repo, gitea.ListReleasesOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		return nil, err
	}
	if len(rl) == 0 {
		return nil, fmt.Errorf("Repo does not have any release")
	}
	for _, r := range rl {
		if r.TagName == tag {
			return r, nil
		}
	}
	return nil, fmt.Errorf("Release tag does not exist")
}
