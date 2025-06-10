// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"context"

	"code.gitea.io/tea/cmd/flags"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdIssuesReopen represents a sub command of issues to open an issue
var CmdIssuesReopen = cli.Command{
	Name:        "reopen",
	Aliases:     []string{"open"},
	Usage:       "Change state of one or more issues to 'open'",
	Description: `Change state of one or more issues to 'open'`,
	ArgsUsage:   "<issue index> [<issue index>...]",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		var s = gitea.StateOpen
		return editIssueState(ctx, cmd, gitea.EditIssueOption{State: &s})
	},
	Flags: flags.AllDefaultFlags,
}
