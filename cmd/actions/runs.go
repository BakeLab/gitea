// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/actions/runs"

	"github.com/urfave/cli/v3"
)

// CmdActionsRuns represents the actions runs command
var CmdActionsRuns = cli.Command{
	Name:        "runs",
	Aliases:     []string{"run"},
	Usage:       "Manage workflow runs",
	Description: "List, view, and manage workflow runs for repository actions",
	Action:      runRunsDefault,
	Commands: []*cli.Command{
		&runs.CmdRunsList,
		&runs.CmdRunsView,
		&runs.CmdRunsDelete,
		&runs.CmdRunsLogs,
	},
}

func runRunsDefault(ctx stdctx.Context, cmd *cli.Command) error {
	return runs.RunRunsList(ctx, cmd)
}
