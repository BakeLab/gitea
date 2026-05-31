// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"gitea.dev/tea/cmd/comments"

	"github.com/urfave/cli/v3"
)

// CmdComments is the top-level command for managing comments on issues and pull requests.
var CmdComments = cli.Command{
	Name:     "comments",
	Aliases:  []string{"comment", "c"},
	Category: catEntities,
	Usage:    "Manage comments on issues and pull requests",
	Description: `Manage comments on issues and pull requests.

When invoked with an issue/PR index and an optional body, behaves like 'tea comments add'
(this preserves the historical 'tea comment <idx> "<body>"' shorthand).`,
	ArgsUsage: "<issue / pr index> [<comment body>]",
	Action:    comments.RunCommentsAdd,
	Flags:     comments.CmdCommentsAdd.Flags,
	Commands: []*cli.Command{
		&comments.CmdCommentsAdd,
		&comments.CmdCommentsList,
		&comments.CmdCommentsEdit,
		&comments.CmdCommentsDelete,
	},
}
