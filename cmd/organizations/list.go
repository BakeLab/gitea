// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package organizations

import (
	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"github.com/urfave/cli/v3"
)

// CmdOrganizationList represents a sub command of organizations to list users organizations
var CmdOrganizationList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List Organizations",
	Description: "List users organizations",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunOrganizationList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunOrganizationList list user organizations
func RunOrganizationList(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	client := ctx.Login.Client()

	userOrganizations, _, err := client.ListUserOrgs(ctx.Login.User, gitea.ListOrgsOptions{
		ListOptions: flags.GetListOptions(),
	})
	if err != nil {
		return err
	}

	print.OrganizationsList(userOrganizations, ctx.Output)

	return nil
}
