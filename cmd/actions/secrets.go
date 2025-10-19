// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/actions/secrets"

	"github.com/urfave/cli/v3"
)

// CmdActionsSecrets represents the actions secrets command
var CmdActionsSecrets = cli.Command{
	Name:        "secrets",
	Aliases:     []string{"secret"},
	Usage:       "Manage repository action secrets",
	Description: "Manage secrets used by repository actions and workflows",
	Action:      runSecretsDefault,
	Commands: []*cli.Command{
		&secrets.CmdSecretsList,
		&secrets.CmdSecretsCreate,
		&secrets.CmdSecretsDelete,
	},
}

func runSecretsDefault(ctx stdctx.Context, cmd *cli.Command) error {
	return secrets.RunSecretsList(ctx, cmd)
}
