// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	"fmt"

	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"
	"github.com/urfave/cli/v3"
)

var msIssuesFieldsFlag = flags.FieldsFlag(print.IssueFields, []string{
	"index", "kind", "title", "state", "updated", "labels",
})

// CmdMilestonesIssues represents a sub command of milestones to manage issue/pull of an milestone
var CmdMilestonesIssues = cli.Command{
	Name:        "issues",
	Aliases:     []string{"i"},
	Usage:       "manage issue/pull of an milestone",
	Description: "manage issue/pull of an milestone",
	ArgsUsage:   "<milestone name>",
	Action:      runMilestoneIssueList,
	Commands: []*cli.Command{
		&CmdMilestoneAddIssue,
		&CmdMilestoneRemoveIssue,
	},
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:        "state",
			Usage:       "Filter by issue state (all|open|closed)",
			DefaultText: "open",
		},
		&cli.StringFlag{
			Name:  "kind",
			Usage: "Filter by kind (issue|pull)",
		},
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
		msIssuesFieldsFlag,
	}, flags.AllDefaultFlags...),
}

// CmdMilestoneAddIssue represents a sub command of milestone issues to add an issue/pull to an milestone
var CmdMilestoneAddIssue = cli.Command{
	Name:        "add",
	Aliases:     []string{"a"},
	Usage:       "Add an issue/pull to an milestone",
	Description: "Add an issue/pull to an milestone",
	ArgsUsage:   "<milestone name> <issue/pull index>",
	Action:      runMilestoneIssueAdd,
	Flags:       flags.AllDefaultFlags,
}

// CmdMilestoneRemoveIssue represents a sub command of milestones to remove an issue/pull from an milestone
var CmdMilestoneRemoveIssue = cli.Command{
	Name:        "remove",
	Aliases:     []string{"r"},
	Usage:       "Remove an issue/pull to an milestone",
	Description: "Remove an issue/pull to an milestone",
	ArgsUsage:   "<milestone name> <issue/pull index>",
	Action:      runMilestoneIssueRemove,
	Flags:       flags.AllDefaultFlags,
}

func runMilestoneIssueList(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	state, err := flags.ParseState(ctx.String("state"))
	if err != nil {
		return err
	}

	kind, err := flags.ParseIssueKind(ctx.String("kind"), gitea.IssueTypeAll)
	if err != nil {
		return err
	}

	if ctx.Args().Len() != 1 {
		return fmt.Errorf("milestone name is required")
	}

	milestone := ctx.Args().First()
	// make sure milestone exist
	_, _, err = client.GetMilestoneByName(ctx.Owner, ctx.Repo, milestone)
	if err != nil {
		return err
	}

	issues, _, err := client.ListRepoIssues(ctx.Owner, ctx.Repo, gitea.ListIssueOption{
		ListOptions: flags.GetListOptions(cmd),
		Milestones:  []string{milestone},
		Type:        kind,
		State:       state,
	})
	if err != nil {
		return err
	}

	fields, err := msIssuesFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}
	return print.IssuesPullsList(issues, ctx.Output, fields)
}

func runMilestoneIssueAdd(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()
	if ctx.Args().Len() != 2 {
		return fmt.Errorf("need two arguments")
	}

	mileName := ctx.Args().Get(0)
	issueIndex := ctx.Args().Get(1)
	idx, err := utils.ArgToIndex(issueIndex)
	if err != nil {
		return err
	}

	// make sure milestone exist
	mile, _, err := client.GetMilestoneByName(ctx.Owner, ctx.Repo, mileName)
	if err != nil {
		return fmt.Errorf("failed to get milestone '%s': %w", mileName, err)
	}

	_, _, err = client.EditIssue(ctx.Owner, ctx.Repo, idx, gitea.EditIssueOption{
		Milestone: &mile.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to add issue #%d to milestone '%s': %w", idx, mileName, err)
	}
	return nil
}

func runMilestoneIssueRemove(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()
	if ctx.Args().Len() != 2 {
		return fmt.Errorf("need two arguments")
	}

	mileName := ctx.Args().Get(0)
	issueIndex := ctx.Args().Get(1)
	idx, err := utils.ArgToIndex(issueIndex)
	if err != nil {
		return fmt.Errorf("invalid issue index '%s': %w", issueIndex, err)
	}

	issue, _, err := client.GetIssue(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return fmt.Errorf("failed to get issue #%d: %w", idx, err)
	}

	if issue.Milestone == nil {
		return fmt.Errorf("issue #%d is not assigned to a milestone", idx)
	}

	if issue.Milestone.Title != mileName {
		return fmt.Errorf("issue #%d is assigned to milestone '%s', not '%s'", idx, issue.Milestone.Title, mileName)
	}

	zero := int64(0)
	_, _, err = client.EditIssue(ctx.Owner, ctx.Repo, idx, gitea.EditIssueOption{
		Milestone: &zero,
	})
	if err != nil {
		return fmt.Errorf("failed to remove issue #%d from milestone '%s': %w", idx, mileName, err)
	}
	return nil
}
