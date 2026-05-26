// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package organizations

import (
	stdctx "context"

	"gitea.dev/sdk"
	"github.com/urfave/cli/v3"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
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
func RunOrganizationList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	client := ctx.Login.Client()

	userOrganizations, _, err := client.Organizations.ListUserOrgs(requestCtx, ctx.Login.User, gitea.ListOrgsOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.OrganizationsList(userOrganizations, ctx.Output)
}
