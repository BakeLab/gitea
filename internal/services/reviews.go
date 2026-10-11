// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Review is a stable review result.
type Review struct {
	ID            int64  `json:"id"`
	Body          string `json:"body"`
	State         string `json:"state"`
	Author        string `json:"author"`
	URL           string `json:"url"`
	Commit        string `json:"commit"`
	SubmittedAt   string `json:"submitted_at"`
	CommentsCount int    `json:"comments_count"`
	Dismissed     bool   `json:"dismissed"`
}

func reviewFrom(review *gitea.PullReview) Review {
	return Review{
		ID: review.ID, Body: review.Body, State: string(review.State), Author: login(review.Reviewer), URL: review.HTMLURL,
		Commit: review.CommitID, SubmittedAt: timestamp(review.Submitted), CommentsCount: review.CodeCommentsCount, Dismissed: review.Dismissed,
	}
}

// ReviewComment describes an inline review conversation.
type ReviewComment struct {
	ID        int64  `json:"id"`
	ReviewID  int64  `json:"review_id"`
	Body      string `json:"body"`
	Author    string `json:"author"`
	Resolver  string `json:"resolver"`
	URL       string `json:"url"`
	Path      string `json:"path"`
	Line      int64  `json:"line"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func reviewCommentFrom(comment *gitea.PullReviewComment) ReviewComment {
	return ReviewComment{
		ID: comment.ID, ReviewID: comment.ReviewID, Body: comment.Body, Author: login(comment.Reviewer), Resolver: login(comment.Resolver),
		URL: comment.HTMLURL, Path: comment.Path, Line: int64(comment.LineNum), CreatedAt: timestamp(comment.Created), UpdatedAt: timestamp(comment.Updated),
	}
}

// InlineComment supplies an explicit file position for review creation.
type InlineComment struct {
	Path    string `json:"path"`
	Body    string `json:"body"`
	OldLine int64  `json:"old_position"`
	NewLine int64  `json:"new_position"`
}

// Validate requires a path, a body, and exactly one positive line position.
func (c InlineComment) Validate() error {
	if strings.TrimSpace(c.Path) == "" || strings.TrimSpace(c.Body) == "" || c.OldLine < 0 || c.NewLine < 0 || (c.OldLine == 0) == (c.NewLine == 0) {
		return errors.New("each inline comment requires path, body, and exactly one positive old_position or new_position")
	}
	return nil
}

// ReviewOptions supplies collected review text and inline comments.
type ReviewOptions struct {
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Commit   string          `json:"commit_id,omitempty"`
	Comments []InlineComment `json:"comments,omitempty"`
}

// ReviewSubmitOptions preserves existing draft text when Body is omitted.
type ReviewSubmitOptions struct {
	Event string
	Body  *string
}

// Reviews manages pull request reviews.
type Reviews struct {
	Client     *transport.Client
	Repository string
}

// List returns a non-nil page of reviews.
func (s Reviews) List(ctx context.Context, n int64, limit, page int) ([]Review, error) {
	reviews, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.ListPullReviews(ctx, owner, name, n, gitea.ListPullReviewsOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(reviews, reviewFrom), nil
}

// View retrieves a review belonging to the selected pull request.
func (s Reviews) View(ctx context.Context, n, id int64) (Review, error) {
	return repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.GetPullReview(ctx, owner, name, n, id)
	})
}

// Create submits a review.
func (s Reviews) Create(ctx context.Context, n int64, opts ReviewOptions) (Review, error) {
	option := gitea.CreatePullReviewOptions{State: gitea.ReviewStateType(opts.Event), Body: opts.Body, CommitID: opts.Commit}
	for _, comment := range opts.Comments {
		option.Comments = append(option.Comments, gitea.CreatePullReviewComment{
			Path: comment.Path, Body: comment.Body, OldLineNum: comment.OldLine, NewLineNum: comment.NewLine,
		})
	}
	return repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.CreatePullReview(ctx, owner, name, n, option)
	})
}

// Submit publishes an existing pending review with explicitly collected input.
func (s Reviews) Submit(ctx context.Context, n, id int64, opts ReviewSubmitOptions) (Review, error) {
	var body string
	if opts.Body == nil {
		current, err := s.View(ctx, n, id)
		if err != nil {
			return Review{}, err
		}
		body = current.Body
	} else {
		body = *opts.Body
	}
	option := gitea.SubmitPullReviewOptions{State: gitea.ReviewStateType(opts.Event), Body: body}
	if option.State != gitea.ReviewStateApproved && strings.TrimSpace(body) == "" {
		// TODO(sdk): SubmitPullReview requires a body unless the event is APPROVED, but Gitea accepts
		// an empty body when the pending review has inline comments.
		var review gitea.PullReview
		endpoint := fmt.Sprintf("repos/%s/pulls/%d/reviews/%d", s.Repository, n, id)
		if err := sendJSON(ctx, s.Client, http.MethodPost, endpoint, option, &review); err != nil {
			return Review{}, err
		}
		return reviewFrom(&review), nil
	}
	return repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.SubmitPullReview(ctx, owner, name, n, id, option)
	})
}

// Delete removes one review.
func (s Reviews) Delete(ctx context.Context, n, id int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.PullRequests.DeletePullReview(ctx, owner, name, n, id)
	})
}

// Dismiss changes a review's contribution to approval requirements and returns the review.
func (s Reviews) Dismiss(ctx context.Context, n, id int64, dismiss bool, message string) (Review, error) {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if dismiss {
			return c.PullRequests.DismissPullReview(ctx, owner, name, n, id, gitea.DismissPullReviewOptions{Message: message})
		}
		return c.PullRequests.UnDismissPullReview(ctx, owner, name, n, id)
	})
	if err != nil {
		return Review{}, err
	}
	review, err := s.View(ctx, n, id)
	if err != nil {
		return Review{}, fmt.Errorf("the review was updated, but reading it failed: %w", err)
	}
	return review, nil
}

// Comments flattens the API's unpaginated per-review collections in ID order.
func (s Reviews) Comments(ctx context.Context, n, reviewID int64, limit, page int) ([]ReviewComment, error) {
	reviews := []Review{{ID: reviewID}}
	if reviewID == 0 {
		var err error
		reviews, err = listAll(func(page int) ([]Review, error) { return s.List(ctx, n, 100, page) })
		if err != nil {
			return nil, err
		}
	}
	results, err := s.reviewComments(ctx, n, reviews, reviewID == 0)
	if err != nil {
		return nil, err
	}
	return pageOf(results, limit, page)
}

// Thread returns every review and every inline review comment of a pull request.
func (s Reviews) Thread(ctx context.Context, n int64) ([]Review, []ReviewComment, error) {
	reviews, err := listAll(func(page int) ([]Review, error) { return s.List(ctx, n, 100, page) })
	if err != nil {
		return nil, nil, err
	}
	comments, err := s.reviewComments(ctx, n, reviews, true)
	return reviews, comments, err
}

func (s Reviews) reviewComments(ctx context.Context, n int64, reviews []Review, skipEmpty bool) ([]ReviewComment, error) {
	results := []ReviewComment{}
	for _, review := range reviews {
		if skipEmpty && review.CommentsCount == 0 {
			continue
		}
		comments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.PullReviewComment, *gitea.Response, error) {
			return c.PullRequests.ListPullReviewComments(ctx, owner, name, n, review.ID)
		})
		if err != nil {
			return nil, err
		}
		results = append(results, convertAll(comments, reviewCommentFrom)...)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

// Resolve marks a review conversation resolved or unresolved.
func (s Reviews) Resolve(ctx context.Context, id int64, resolved bool) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if resolved {
			return c.PullRequests.ResolvePullReviewComment(ctx, owner, name, id)
		}
		return c.PullRequests.UnresolvePullReviewComment(ctx, owner, name, id)
	})
}

// Reply adds a reply to a review comment.
func (s Reviews) Reply(ctx context.Context, n, id int64, body string) (ReviewComment, error) {
	return repoConvert(ctx, s.Client, s.Repository, reviewCommentFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReviewComment, *gitea.Response, error) {
		return c.PullRequests.CreatePullReviewCommentReply(ctx, owner, name, n, id, gitea.CreatePullReviewCommentReplyOptions{Body: body})
	})
}
