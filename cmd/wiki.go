// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	wikiCmd "gitea.dev/tea/cmd/wiki"
	"github.com/urfave/cli/v3"
)

// CmdWiki represents the wiki command.
var CmdWiki = cli.Command{
	Name:        "wiki",
	Category:    catEntities,
	Usage:       "Manage repository wiki pages",
	Description: "Manage repository wiki pages",
	Action:      runWiki,
	Commands: []*cli.Command{
		&wikiCmd.CmdWikiList,
		&wikiCmd.CmdWikiView,
		&wikiCmd.CmdWikiRevisions,
		&wikiCmd.CmdWikiCreate,
		&wikiCmd.CmdWikiEdit,
		&wikiCmd.CmdWikiDelete,
	},
	Flags: append([]cli.Flag{}, wikiCmd.CmdWikiList.Flags...),
}

func runWiki(_ stdctx.Context, cmd *cli.Command) error {
	return cli.ShowSubcommandHelp(cmd)
}
