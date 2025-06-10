// Copyright 2024 The Gitea Authors. All rights reserved.
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

// CmdBranchesProtectFlags Flags for command protect/unprotect
var CmdBranchesProtectFlags = append([]cli.Flag{
	branchFieldsFlag,
	&flags.PaginationPageFlag,
	&flags.PaginationLimitFlag,
}, flags.AllDefaultFlags...)

// CmdBranchesProtect represents a sub command of branches to protect a branch
var CmdBranchesProtect = cli.Command{
	Name:        "protect",
	Aliases:     []string{"P"},
	Usage:       "Protect branches",
	Description: `Block actions push/merge on specified branches`,
	ArgsUsage:   "<branch>",
	Action:      RunBranchesProtect,
	Flags:       CmdBranchesProtectFlags,
}

// CmdBranchesUnprotect represents a sub command of branches to protect a branch
var CmdBranchesUnprotect = cli.Command{
	Name:        "unprotect",
	Aliases:     []string{"U"},
	Usage:       "Unprotect branches",
	Description: `Suppress existing protections on specified branches`,
	ArgsUsage:   "<branch>",
	Action:      RunBranchesProtect,
	Flags:       CmdBranchesProtectFlags,
}

// RunBranchesProtect function to protect/unprotect a list of branches
func RunBranchesProtect(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	if !cmd.Args().Present() {
		return fmt.Errorf("must specify at least one branch")
	}

	owner := ctx.Owner
	if ctx.IsSet("owner") {
		owner = ctx.String("owner")
	}

	for _, branch := range ctx.Args().Slice() {

		var err error
		command := ctx.Command.Name
		if command == "protect" {
			_, _, err = ctx.Login.Client().CreateBranchProtection(owner, ctx.Repo, gitea.CreateBranchProtectionOption{
				BranchName:                    branch,
				RuleName:                      "",
				EnablePush:                    false,
				EnablePushWhitelist:           false,
				PushWhitelistUsernames:        []string{},
				PushWhitelistTeams:            []string{},
				PushWhitelistDeployKeys:       false,
				EnableMergeWhitelist:          false,
				MergeWhitelistUsernames:       []string{},
				MergeWhitelistTeams:           []string{},
				EnableStatusCheck:             false,
				StatusCheckContexts:           []string{},
				RequiredApprovals:             1,
				EnableApprovalsWhitelist:      false,
				ApprovalsWhitelistUsernames:   []string{},
				ApprovalsWhitelistTeams:       []string{},
				BlockOnRejectedReviews:        false,
				BlockOnOfficialReviewRequests: false,
				BlockOnOutdatedBranch:         false,
				DismissStaleApprovals:         false,
				RequireSignedCommits:          false,
				ProtectedFilePatterns:         "",
				UnprotectedFilePatterns:       "",
			})
		} else if command == "unprotect" {
			_, err = ctx.Login.Client().DeleteBranchProtection(owner, ctx.Repo, branch)
		} else {
			return fmt.Errorf("command %s is not supported", command)
		}

		if err != nil {
			return err
		}
	}

	return nil
}
