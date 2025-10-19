// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/actions/variables"

	"github.com/urfave/cli/v3"
)

// CmdActionsVariables represents the actions variables command
var CmdActionsVariables = cli.Command{
	Name:        "variables",
	Aliases:     []string{"variable", "vars", "var"},
	Usage:       "Manage repository action variables",
	Description: "Manage variables used by repository actions and workflows",
	Action:      runVariablesDefault,
	Commands: []*cli.Command{
		&variables.CmdVariablesList,
		&variables.CmdVariablesSet,
		&variables.CmdVariablesDelete,
	},
}

func runVariablesDefault(ctx stdctx.Context, cmd *cli.Command) error {
	return variables.RunVariablesList(ctx, cmd)
}
