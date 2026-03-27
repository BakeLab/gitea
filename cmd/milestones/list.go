// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

var fieldsFlag = flags.FieldsFlag(print.MilestoneFields, []string{
	"title", "items", "duedate",
})

// CmdMilestonesList represents a sub command of milestones to list milestones
var CmdMilestonesList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List milestones of the repository",
	Description: `List milestones of the repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunMilestonesList,
	Flags: append([]cli.Flag{
		fieldsFlag,
		&cli.StringFlag{
			Name:        "state",
			Usage:       "Filter by milestone state (all|open|closed)",
			DefaultText: "open",
		},
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunMilestonesList list milestones
func RunMilestonesList(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	fields, err := fieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	state, err := flags.ParseState(ctx.String("state"))
	if err != nil {
		return err
	}
	if state == gitea.StateAll && !cmd.IsSet("fields") {
		fields = append(fields, "state")
	}

	client := ctx.Login.Client()
	milestones, _, err := client.ListRepoMilestones(ctx.Owner, ctx.Repo, gitea.ListMilestoneOption{
		ListOptions: flags.GetListOptions(cmd),
		State:       state,
	})
	if err != nil {
		return err
	}

	return print.MilestonesList(milestones, ctx.Output, fields)
}
