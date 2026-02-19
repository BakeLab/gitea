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
	"code.gitea.io/tea/cmd/pulls"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/interact"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

type pullLabelData struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type pullReviewData struct {
	ID       int64                 `json:"id"`
	Reviewer string                `json:"reviewer"`
	State    gitea.ReviewStateType `json:"state"`
	Body     string                `json:"body"`
	Created  time.Time             `json:"created"`
}

type pullCommentData struct {
	ID      int64     `json:"id"`
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

type pullData struct {
	ID        int64             `json:"id"`
	Index     int64             `json:"index"`
	Title     string            `json:"title"`
	State     gitea.StateType   `json:"state"`
	Created   *time.Time        `json:"created"`
	Updated   *time.Time        `json:"updated"`
	Labels    []pullLabelData   `json:"labels"`
	User      string            `json:"user"`
	Body      string            `json:"body"`
	Assignees []string          `json:"assignees"`
	URL       string            `json:"url"`
	Base      string            `json:"base"`
	Head      string            `json:"head"`
	HeadSha   string            `json:"headSha"`
	DiffURL   string            `json:"diffUrl"`
	Mergeable bool              `json:"mergeable"`
	HasMerged bool              `json:"hasMerged"`
	MergedAt  *time.Time        `json:"mergedAt"`
	MergedBy  string            `json:"mergedBy,omitempty"`
	ClosedAt  *time.Time        `json:"closedAt"`
	Reviews   []pullReviewData  `json:"reviews"`
	Comments  []pullCommentData `json:"comments"`
}

// CmdPulls is the main command to operate on PRs
var CmdPulls = cli.Command{
	Name:        "pulls",
	Aliases:     []string{"pull", "pr"},
	Category:    catEntities,
	Usage:       "Manage and checkout pull requests",
	Description: `Lists PRs when called without argument. If PR index is provided, will show it in detail.`,
	ArgsUsage:   "[<pull index>]",
	Action:      runPulls,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:  "comments",
			Usage: "Whether to display comments (will prompt if not provided & run interactively)",
		},
	}, pulls.CmdPullsList.Flags...),
	Commands: []*cli.Command{
		&pulls.CmdPullsList,
		&pulls.CmdPullsCheckout,
		&pulls.CmdPullsClean,
		&pulls.CmdPullsCreate,
		&pulls.CmdPullsClose,
		&pulls.CmdPullsReopen,
		&pulls.CmdPullsReview,
		&pulls.CmdPullsApprove,
		&pulls.CmdPullsReject,
		&pulls.CmdPullsMerge,
	},
}

func runPulls(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runPullDetail(ctx, cmd, cmd.Args().First())
	}
	return pulls.RunPullsList(ctx, cmd)
}

func runPullDetail(_ stdctx.Context, cmd *cli.Command, index string) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	idx, err := utils.ArgToIndex(index)
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	pr, _, err := client.GetPullRequest(ctx.Owner, ctx.Repo, idx)
	if err != nil {
		return err
	}

	reviews, _, err := client.ListPullReviews(ctx.Owner, ctx.Repo, idx, gitea.ListPullReviewsOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		fmt.Printf("error while loading reviews: %v\n", err)
	}

	if ctx.IsSet("output") {
		switch ctx.String("output") {
		case "json":
			return runPullDetailAsJSON(ctx, pr, reviews)
		}
	}

	ci, _, err := client.GetCombinedStatus(ctx.Owner, ctx.Repo, pr.Head.Sha)
	if err != nil {
		fmt.Printf("error while loading CI: %v\n", err)
	}

	print.PullDetails(pr, reviews, ci)

	if pr.Comments > 0 {
		err = interact.ShowCommentsMaybeInteractive(ctx, idx, pr.Comments)
		if err != nil {
			fmt.Printf("error loading comments: %v\n", err)
		}
	}

	return nil
}

func runPullDetailAsJSON(ctx *context.TeaContext, pr *gitea.PullRequest, reviews []*gitea.PullReview) error {
	c := ctx.Login.Client()
	opts := gitea.ListIssueCommentOptions{ListOptions: flags.GetListOptions()}

	labelSlice := make([]pullLabelData, 0, len(pr.Labels))
	for _, label := range pr.Labels {
		labelSlice = append(labelSlice, pullLabelData{label.Name, label.Color, label.Description})
	}

	assigneesSlice := make([]string, 0, len(pr.Assignees))
	for _, assignee := range pr.Assignees {
		assigneesSlice = append(assigneesSlice, assignee.UserName)
	}

	reviewsSlice := make([]pullReviewData, 0, len(reviews))
	for _, review := range reviews {
		reviewsSlice = append(reviewsSlice, pullReviewData{
			ID:       review.ID,
			Reviewer: review.Reviewer.UserName,
			State:    review.State,
			Body:     review.Body,
			Created:  review.Submitted,
		})
	}

	mergedBy := ""
	if pr.MergedBy != nil {
		mergedBy = pr.MergedBy.UserName
	}

	pullSlice := pullData{
		ID:        pr.ID,
		Index:     pr.Index,
		Title:     pr.Title,
		State:     pr.State,
		Created:   pr.Created,
		Updated:   pr.Updated,
		User:      pr.Poster.UserName,
		Body:      pr.Body,
		Labels:    labelSlice,
		Assignees: assigneesSlice,
		URL:       pr.HTMLURL,
		Base:      pr.Base.Ref,
		Head:      pr.Head.Ref,
		HeadSha:   pr.Head.Sha,
		DiffURL:   pr.DiffURL,
		Mergeable: pr.Mergeable,
		HasMerged: pr.HasMerged,
		MergedAt:  pr.Merged,
		MergedBy:  mergedBy,
		ClosedAt:  pr.Closed,
		Reviews:   reviewsSlice,
		Comments:  make([]pullCommentData, 0),
	}

	if ctx.Bool("comments") {
		comments, _, err := c.ListIssueComments(ctx.Owner, ctx.Repo, pr.Index, opts)
		if err != nil {
			return err
		}

		pullSlice.Comments = make([]pullCommentData, 0, len(comments))
		for _, comment := range comments {
			pullSlice.Comments = append(pullSlice.Comments, pullCommentData{
				ID:      comment.ID,
				Author:  comment.Poster.UserName,
				Body:    comment.Body,
				Created: comment.Created,
			})
		}
	}

	jsonData, err := json.MarshalIndent(pullSlice, "", "\t")
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(ctx.Writer, "%s\n", jsonData)

	return err
}
