// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"fmt"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/cmd/issues"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

type labelData = detailLabelData

type issueData struct {
	ID        int64           `json:"id"`
	Index     int64           `json:"index"`
	Title     string          `json:"title"`
	State     gitea.StateType `json:"state"`
	Created   time.Time       `json:"created"`
	Labels    []labelData     `json:"labels"`
	User      string          `json:"user"`
	Body      string          `json:"body"`
	Assignees []string        `json:"assignees"`
	URL       string          `json:"url"`
	ClosedAt  *time.Time      `json:"closedAt"`
	Comments  []commentData   `json:"comments"`
}

type issueDetailClient interface {
	GetIssue(owner, repo string, index int64) (*gitea.Issue, *gitea.Response, error)
	GetIssueReactions(owner, repo string, index int64) ([]*gitea.Reaction, *gitea.Response, error)
}

type issueCommentClient interface {
	ListIssueComments(owner, repo string, index int64, opt gitea.ListIssueCommentOptions) ([]*gitea.Comment, *gitea.Response, error)
}

type commentData = detailCommentData

// CmdIssues represents to login a gitea server.
var CmdIssues = cli.Command{
	Name:        "issues",
	Aliases:     []string{"issue", "i"},
	Category:    catEntities,
	Usage:       "List, create and update issues",
	Description: `Lists issues when called without argument. If issue index is provided, will show it in detail.`,
	ArgsUsage:   "[<issue index>]",
	Action:      runIssues,
	Commands: []*cli.Command{
		&issues.CmdIssuesList,
		&issues.CmdIssuesCreate,
		&issues.CmdIssuesEdit,
		&issues.CmdIssuesReopen,
		&issues.CmdIssuesClose,
	},
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:  "comments",
			Usage: "Whether to display comments (will prompt if not provided & run interactively)",
		},
	}, issues.CmdIssuesList.Flags...),
}

func runIssues(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runIssueDetail(ctx, cmd, cmd.Args().First())
	}
	return issues.RunIssuesList(ctx, cmd)
}

func runIssueDetail(_ stdctx.Context, cmd *cli.Command, index string) error {
	ctx, idx, err := resolveIssueDetailContext(cmd, index)
	if err != nil {
		return err
	}

	return runIssueDetailWithClient(ctx, idx, ctx.Login.Client())
}

func resolveIssueDetailContext(cmd *cli.Command, index string) (*context.TeaContext, int64, error) {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return nil, 0, err
	}
	if ctx.IsSet("owner") {
		ctx.Owner = ctx.String("owner")
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return nil, 0, err
	}

	idx, err := utils.ArgToIndex(index)
	if err != nil {
		return nil, 0, err
	}

	return ctx, idx, nil
}

func runIssueDetailWithClient(ctx *context.TeaContext, idx int64, client issueDetailClient) error {
	issue, _, err := client.GetIssue(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return err
	}
	reactions, _, err := client.GetIssueReactions(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return err
	}

	if ctx.IsSet("output") {
		switch ctx.String("output") {
		case "json":
			return runIssueDetailAsJSON(ctx, issue)
		}
	}

	print.IssueDetails(issue, reactions)

	if issue.Comments > 0 {
		err = interact.ShowCommentsMaybeInteractive(ctx, idx, issue.Comments)
		if err != nil {
			return fmt.Errorf("error loading comments: %v", err)
		}
	}

	return nil
}

func runIssueDetailAsJSON(ctx *context.TeaContext, issue *gitea.Issue) error {
	return runIssueDetailAsJSONWithClient(ctx, issue, ctx.Login.Client())
}

func runIssueDetailAsJSONWithClient(ctx *context.TeaContext, issue *gitea.Issue, c issueCommentClient) error {
	opts := gitea.ListIssueCommentOptions{ListOptions: flags.GetListOptions(ctx.Command)}
	comments := []*gitea.Comment{}

	if ctx.Bool("comments") {
		var err error
		comments, _, err = c.ListIssueComments(ctx.Owner, ctx.Repo, issue.Index, opts)
		if err != nil {
			return err
		}
	}

	return writeIndentedJSON(ctx.Writer, buildIssueData(issue, comments))
}

func buildIssueData(issue *gitea.Issue, comments []*gitea.Comment) issueData {
	return issueData{
		ID:        issue.ID,
		Index:     issue.Index,
		Title:     issue.Title,
		State:     issue.State,
		Created:   issue.Created,
		User:      username(issue.Poster),
		Body:      issue.Body,
		Labels:    buildDetailLabels(issue.Labels),
		Assignees: buildDetailAssignees(issue.Assignees),
		URL:       issue.HTMLURL,
		ClosedAt:  issue.Closed,
		Comments:  buildDetailComments(comments),
	}
}
