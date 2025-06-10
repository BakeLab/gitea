// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package branches

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

var branchFieldsFlag = flags.FieldsFlag(print.BranchFields, []string{
	"name", "protected", "user-can-merge", "user-can-push",
})

// CmdBranchesListFlags Flags for command list
var CmdBranchesListFlags = append([]cli.Flag{
	branchFieldsFlag,
	&flags.PaginationPageFlag,
	&flags.PaginationLimitFlag,
}, flags.AllDefaultFlags...)

// CmdBranchesList represents a sub command of branches to list branches
var CmdBranchesList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List branches of the repository",
	Description: `List branches of the repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunBranchesList,
	Flags:       CmdBranchesListFlags,
}

// RunBranchesList list branches
func RunBranchesList(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	owner := ctx.Owner
	if ctx.IsSet("owner") {
		owner = ctx.String("owner")
	}

	var branches []*gitea.Branch
	var protections []*gitea.BranchProtection
	var err error
	branches, _, err = ctx.Login.Client().ListRepoBranches(owner, ctx.Repo, gitea.ListRepoBranchesOptions{
		ListOptions: ctx.GetListOptions(),
	})

	if err != nil {
		return err
	}

	protections, _, err = ctx.Login.Client().ListBranchProtections(owner, ctx.Repo, gitea.ListBranchProtectionsOptions{
		ListOptions: ctx.GetListOptions(),
	})

	if err != nil {
		return err
	}

	fields, err := branchFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	print.BranchesList(branches, protections, ctx.Output, fields)
	return nil
}
