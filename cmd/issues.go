// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"encoding/json"
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

type labelData struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

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

type commentData struct {
	ID      int64     `json:"id"`
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

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
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	idx, err := utils.ArgToIndex(index)
	if err != nil {
		return err
	}
	client := ctx.Login.Client()
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
	c := ctx.Login.Client()
	opts := gitea.ListIssueCommentOptions{ListOptions: flags.GetListOptions()}

	labelSlice := make([]labelData, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		labelSlice = append(labelSlice, labelData{label.Name, label.Color, label.Description})
	}

	assigneesSlice := make([]string, 0, len(issue.Assignees))
	for _, assignee := range issue.Assignees {
		assigneesSlice = append(assigneesSlice, assignee.UserName)
	}

	issueSlice := issueData{
		ID:        issue.ID,
		Index:     issue.Index,
		Title:     issue.Title,
		State:     issue.State,
		Created:   issue.Created,
		User:      issue.Poster.UserName,
		Body:      issue.Body,
		Labels:    labelSlice,
		Assignees: assigneesSlice,
		URL:       issue.HTMLURL,
		ClosedAt:  issue.Closed,
		Comments:  make([]commentData, 0),
	}

	if ctx.Bool("comments") {
		comments, _, err := c.ListIssueComments(ctx.Owner, ctx.Repo, issue.Index, opts)
		issueSlice.Comments = make([]commentData, 0, len(comments))

		if err != nil {
			return err
		}

		for _, comment := range comments {
			issueSlice.Comments = append(issueSlice.Comments, commentData{
				ID:      comment.ID,
				Author:  comment.Poster.UserName,
				Body:    comment.Body, // Selected Field
				Created: comment.Created,
			})
		}

	}

	jsonData, err := json.MarshalIndent(issueSlice, "", "\t")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(ctx.Writer, "%s\n", jsonData)

	return err
}
