// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
)

// ListPullReviewComments lists all review comments across all reviews for a PR
func ListPullReviewComments(ctx *context.TeaContext, idx int64) ([]*gitea.PullReviewComment, error) {
	c := ctx.Login.Client()

	var reviews []*gitea.PullReview
	for page := 1; ; {
		page_reviews, resp, err := c.ListPullReviews(ctx.Owner, ctx.Repo, idx, gitea.ListPullReviewsOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, page_reviews...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}

	var allComments []*gitea.PullReviewComment
	for _, review := range reviews {
		comments, _, err := c.ListPullReviewComments(ctx.Owner, ctx.Repo, idx, review.ID)
		if err != nil {
			return nil, err
		}
		allComments = append(allComments, comments...)
	}

	return allComments, nil
}

// ResolvePullReviewComment resolves a review comment
func ResolvePullReviewComment(ctx *context.TeaContext, commentID int64) error {
	c := ctx.Login.Client()

	_, err := c.ResolvePullReviewComment(ctx.Owner, ctx.Repo, commentID)
	if err != nil {
		return err
	}

	fmt.Printf("Comment %d resolved\n", commentID)
	return nil
}

// UnresolvePullReviewComment unresolves a review comment
func UnresolvePullReviewComment(ctx *context.TeaContext, commentID int64) error {
	c := ctx.Login.Client()

	_, err := c.UnresolvePullReviewComment(ctx.Owner, ctx.Repo, commentID)
	if err != nil {
		return err
	}

	fmt.Printf("Comment %d unresolved\n", commentID)
	return nil
}
