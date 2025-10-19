// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdSecretsList represents a sub command to list action secrets
var CmdSecretsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List action secrets",
	Description: "List secrets configured for repository actions",
	Action:      RunSecretsList,
	Flags:       flags.AllDefaultFlags,
}

// RunSecretsList list action secrets
func RunSecretsList(ctx stdctx.Context, cmd *cli.Command) error {
	c := context.InitCommand(cmd)
	client := c.Login.Client()

	secrets, _, err := client.ListRepoActionSecret(c.Owner, c.Repo, gitea.ListRepoActionSecretOption{
		ListOptions: flags.GetListOptions(),
	})
	if err != nil {
		return err
	}

	print.ActionSecretsList(secrets, c.Output)
	return nil
}
