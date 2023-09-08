// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"code.gitea.io/tea/cmd/flags"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdPullsReopen reopens a given closed pull request
var CmdPullsReopen = cli.Command{
	Name:        "reopen",
	Aliases:     []string{"open"},
	Usage:       "Change state of one or more pull requests to 'open'",
	Description: `Change state of one or more pull requests to 'open'`,
	ArgsUsage:   "<pull index> [<pull index>...]",
	Action: func(ctx *cli.Context) error {
		var s = gitea.StateOpen
		return editPullState(ctx, gitea.EditPullRequestOption{State: &s})
	},
	Flags: flags.AllDefaultFlags,
}
