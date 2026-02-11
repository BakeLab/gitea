// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/actions/workflows"

	"github.com/urfave/cli/v3"
)

// CmdActionsWorkflows represents the actions workflows command
var CmdActionsWorkflows = cli.Command{
	Name:        "workflows",
	Aliases:     []string{"workflow"},
	Usage:       "Manage repository workflows",
	Description: "List and manage repository action workflows",
	Action:      runWorkflowsDefault,
	Commands: []*cli.Command{
		&workflows.CmdWorkflowsList,
	},
}

func runWorkflowsDefault(ctx stdctx.Context, cmd *cli.Command) error {
	return workflows.RunWorkflowsList(ctx, cmd)
}
