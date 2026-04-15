// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package branches

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdBranchesRenameFlags Flags for command rename
var CmdBranchesRenameFlags = append([]cli.Flag{
	branchFieldsFlag,
	&flags.PaginationPageFlag,
	&flags.PaginationLimitFlag,
}, flags.AllDefaultFlags...)

// CmdBranchesRename represents a sub command of branches to rename a branch
var CmdBranchesRename = cli.Command{
	Name:        "rename",
	Aliases:     []string{"rn"},
	Usage:       "Rename a branch",
	Description: `Rename a branch in a repository`,
	ArgsUsage:   "<old_branch_name> <new_branch_name>",
	Action:      RunBranchesRename,
	Flags:       CmdBranchesRenameFlags,
}

// RunBranchesRename function to rename a branch
func RunBranchesRename(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if err := ValidateRenameArgs(ctx.Args().Slice()); err != nil {
		return err
	}

	oldBranchName := ctx.Args().Get(0)
	newBranchName := ctx.Args().Get(1)

	owner := ctx.Owner
	if ctx.IsSet("owner") {
		owner = ctx.String("owner")
	}

	successful, _, err := ctx.Login.Client().RenameRepoBranch(owner, ctx.Repo, oldBranchName, gitea.RenameRepoBranchOption{
		Name: newBranchName,
	})
	if err != nil {
		return fmt.Errorf("failed to rename branch: %w", err)
	}
	if !successful {
		return fmt.Errorf("failed to rename branch")
	}

	fmt.Printf("Successfully renamed branch '%s' to '%s'\n", oldBranchName, newBranchName)

	return nil
}

// ValidateRenameArgs validates arguments for the rename command
func ValidateRenameArgs(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("must specify exactly two arguments: <old_branch_name> <new_branch_name>")
	}
	return nil
}
