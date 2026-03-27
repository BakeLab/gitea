// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package organizations

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"github.com/urfave/cli/v3"
)

// CmdOrganizationDelete represents a sub command of organizations to delete a given user organization
var CmdOrganizationDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete users Organizations",
	Description: "Delete users organizations",
	ArgsUsage:   "<organization name>",
	Action:      RunOrganizationDelete,
	Flags: []cli.Flag{
		&flags.LoginFlag,
		&flags.RemoteFlag,
	},
}

// RunOrganizationDelete delete user organization
func RunOrganizationDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	client := ctx.Login.Client()

	if ctx.Args().Len() < 1 {
		return fmt.Errorf("organization name is required")
	}

	response, err := client.DeleteOrg(ctx.Args().First())
	if response != nil && response.StatusCode == 404 {
		return fmt.Errorf("organization not found: %s", ctx.Args().First())
	}

	return err
}
