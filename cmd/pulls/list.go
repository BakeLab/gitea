// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"
	"slices"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"github.com/urfave/cli/v3"
)

var pullFieldsFlag = flags.FieldsFlag(print.PullFields, []string{
	"index", "title", "state", "author", "milestone", "updated", "labels",
})

// CmdPullsList represents a sub command of issues to list pulls
var CmdPullsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List pull requests of the repository",
	Description: `List pull requests of the repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunPullsList,
	Flags:       append([]cli.Flag{pullFieldsFlag}, flags.PRListingFlags...),
}

// RunPullsList return list of pulls
func RunPullsList(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	state, err := flags.ParseState(ctx.String("state"))
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	prs, _, err := client.ListRepoPullRequests(ctx.Owner, ctx.Repo, gitea.ListPullRequestsOptions{
		ListOptions: flags.GetListOptions(cmd),
		State:       state,
	})
	if err != nil {
		return err
	}

	fields, err := pullFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	var ciStatuses map[int64]*gitea.CombinedStatus
	if slices.Contains(fields, "ci") {
		ciStatuses = map[int64]*gitea.CombinedStatus{}
		for _, pr := range prs {
			if pr.Head == nil || pr.Head.Sha == "" {
				continue
			}
			ci, _, err := client.GetCombinedStatus(ctx.Owner, ctx.Repo, pr.Head.Sha)
			if err != nil {
				fmt.Printf("error fetching CI status for PR #%d: %v\n", pr.Index, err)
				continue
			}
			ciStatuses[pr.Index] = ci
		}
	}

	return print.PullsList(prs, ctx.Output, fields, ciStatuses)
}
