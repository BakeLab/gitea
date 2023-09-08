// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"fmt"
	"time"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/araddon/dateparse"
	"github.com/urfave/cli/v2"
)

var issueFieldsFlag = flags.FieldsFlag(print.IssueFields, []string{
	"index", "title", "state", "author", "milestone", "labels", "owner", "repo",
})

// CmdIssuesList represents a sub command of issues to list issues
var CmdIssuesList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List issues of the repository",
	Description: `List issues of the repository`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunIssuesList,
	Flags:       append([]cli.Flag{issueFieldsFlag}, flags.IssueListingFlags...),
}

// RunIssuesList list issues
func RunIssuesList(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)

	state := gitea.StateOpen
	switch ctx.String("state") {
	case "all":
		state = gitea.StateAll
	case "", "open":
		state = gitea.StateOpen
	case "closed":
		state = gitea.StateClosed
	default:
		return fmt.Errorf("unknown state '%s'", ctx.String("state"))
	}

	kind := gitea.IssueTypeIssue
	switch ctx.String("kind") {
	case "", "issues", "issue":
		kind = gitea.IssueTypeIssue
	case "pulls", "pull", "pr":
		kind = gitea.IssueTypePull
	case "all":
		kind = gitea.IssueTypeAll
	default:
		return fmt.Errorf("unknown kind '%s'", ctx.String("kind"))
	}

	var err error
	var from, until time.Time
	if ctx.IsSet("from") {
		from, err = dateparse.ParseLocal(ctx.String("from"))
		if err != nil {
			return err
		}
	}
	if ctx.IsSet("until") {
		until, err = dateparse.ParseLocal(ctx.String("until"))
		if err != nil {
			return err
		}
	}
	owner := ctx.Owner
	if ctx.IsSet("owner") {
		owner = ctx.String("owner")
	}

	// ignore error, as we don't do any input validation on these flags
	labels, _ := flags.LabelFilterFlag.GetValues(cmd)
	milestones, _ := flags.MilestoneFilterFlag.GetValues(cmd)
	var issues []*gitea.Issue
	if ctx.Repo != "" {
		issues, _, err = ctx.Login.Client().ListRepoIssues(owner, ctx.Repo, gitea.ListIssueOption{
			ListOptions: ctx.GetListOptions(),
			State:       state,
			Type:        kind,
			KeyWord:     ctx.String("keyword"),
			CreatedBy:   ctx.String("author"),
			AssignedBy:  ctx.String("assigned-to"),
			MentionedBy: ctx.String("mentions"),
			Labels:      labels,
			Milestones:  milestones,
			Since:       from,
			Before:      until,
		})

		if err != nil {
			return err
		}
	} else {
		issues, _, err = ctx.Login.Client().ListIssues(gitea.ListIssueOption{
			ListOptions: ctx.GetListOptions(),
			State:       state,
			Type:        kind,
			KeyWord:     ctx.String("keyword"),
			CreatedBy:   ctx.String("author"),
			AssignedBy:  ctx.String("assigned-to"),
			MentionedBy: ctx.String("mentions"),
			Labels:      labels,
			Milestones:  milestones,
			Since:       from,
			Before:      until,
			Owner:       owner,
		})

		if err != nil {
			return err
		}
	}

	fields, err := issueFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	print.IssuesPullsList(issues, ctx.Output, fields)
	return nil
}
