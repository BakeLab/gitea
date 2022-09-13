// Copyright 2020 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package milestones

import (
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
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
func RunMilestonesList(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	fields, err := fieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	state := gitea.StateOpen
	switch ctx.String("state") {
	case "all":
		state = gitea.StateAll
		if !cmd.IsSet("fields") { // add to default fields
			fields = append(fields, "state")
		}
	case "closed":
		state = gitea.StateClosed
	}

	client := ctx.Login.Client()
	milestones, _, err := client.ListRepoMilestones(ctx.Owner, ctx.Repo, gitea.ListMilestoneOption{
		ListOptions: ctx.GetListOptions(),
		State:       state,
	})

	if err != nil {
		return err
	}

	print.MilestonesList(milestones, ctx.Output, fields)
	return nil
}
