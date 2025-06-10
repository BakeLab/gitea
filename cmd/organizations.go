// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/organizations"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"github.com/urfave/cli/v3"
)

// CmdOrgs represents handle organization
var CmdOrgs = cli.Command{
	Name:        "organizations",
	Aliases:     []string{"organization", "org"},
	Category:    catEntities,
	Usage:       "List, create, delete organizations",
	Description: "Show organization details",
	ArgsUsage:   "[<organization>]",
	Action:      runOrganizations,
	Commands: []*cli.Command{
		&organizations.CmdOrganizationList,
		&organizations.CmdOrganizationCreate,
		&organizations.CmdOrganizationDelete,
	},
	Flags: organizations.CmdOrganizationList.Flags,
}

func runOrganizations(ctx stdctx.Context, cmd *cli.Command) error {
	teaCtx := context.InitCommand(cmd)
	if teaCtx.Args().Len() == 1 {
		return runOrganizationDetail(teaCtx)
	}
	return organizations.RunOrganizationList(ctx, cmd)
}

func runOrganizationDetail(ctx *context.TeaContext) error {
	org, _, err := ctx.Login.Client().GetOrg(ctx.Args().First())
	if err != nil {
		return err
	}

	print.OrganizationDetails(org)
	return nil
}
