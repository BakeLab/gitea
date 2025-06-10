// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"code.gitea.io/tea/cmd/attachments"
	"code.gitea.io/tea/cmd/flags"

	"github.com/urfave/cli/v3"
)

// CmdReleaseAttachments represents a release attachment (file attachment)
var CmdReleaseAttachments = cli.Command{
	Name:        "assets",
	Aliases:     []string{"asset", "a"},
	Category:    catEntities,
	Usage:       "Manage release assets",
	Description: "Manage release assets",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      attachments.RunReleaseAttachmentList,
	Commands: []*cli.Command{
		&attachments.CmdReleaseAttachmentList,
		&attachments.CmdReleaseAttachmentCreate,
		&attachments.CmdReleaseAttachmentDelete,
	},
	Flags: flags.AllDefaultFlags,
}
