// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachments

import (
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/cmd/releases"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdReleaseAttachmentCreate represents a sub command of Release Attachments to create a release attachment
var CmdReleaseAttachmentCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create one or more release attachments",
	Description: `Create one or more release attachments`,
	ArgsUsage:   "<release-tag> <asset> [<asset>...]",
	Action:      runReleaseAttachmentCreate,
	Flags:       flags.AllDefaultFlags,
}

func runReleaseAttachmentCreate(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	if ctx.Args().Len() < 2 {
		return fmt.Errorf("no release tag or assets specified.\nUsage:\t%s", ctx.Command.UsageText)
	}

	tag := ctx.Args().First()
	if len(tag) == 0 {
		return fmt.Errorf("release tag needed to create attachment")
	}

	release, err := releases.GetReleaseByTag(ctx.Owner, ctx.Repo, tag, client)
	if err != nil {
		return err
	}

	for _, asset := range ctx.Args().Slice()[1:] {
		var file *os.File
		if file, err = os.Open(asset); err != nil {
			return err
		}

		filePath := filepath.Base(asset)

		if _, _, err = ctx.Login.Client().CreateReleaseAttachment(ctx.Owner, ctx.Repo, release.ID, file, filePath); err != nil {
			file.Close()
			return err
		}

		file.Close()
	}

	return nil
}
