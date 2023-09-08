// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"code.gitea.io/tea/cmd/flags"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdPullsClose closes a given open pull request
var CmdPullsClose = cli.Command{
	Name:        "close",
	Usage:       "Change state of one or more pull requests to 'closed'",
	Description: `Change state of one or more pull requests to 'closed'`,
	ArgsUsage:   "<pull index> [<pull index>...]",
	Action: func(ctx *cli.Context) error {
		var s = gitea.StateClosed
		return editPullState(ctx, gitea.EditPullRequestOption{State: &s})
	},
	Flags: flags.AllDefaultFlags,
}
