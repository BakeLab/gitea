// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Comment is the public representation shared by issue and pull request comments.
type Comment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// IssueNumber is the issue or pull request the comment is on.
	IssueNumber int64 `json:"issue_number"`
	// Pull reports a comment on a pull request rather than an issue.
	Pull bool `json:"-"`
}

func commentFrom(comment *gitea.Comment) Comment {
	return Comment{
		ID: comment.ID, Body: comment.Body, URL: comment.HTMLURL, Author: login(comment.Poster),
		CreatedAt: timestamp(comment.Created), UpdatedAt: timestamp(comment.Updated), Pull: comment.PRURL != "",
		IssueNumber: lastPathNumber(comment.IssueURL + comment.PRURL),
	}
}

// Comments implements discussion comments under their owning repository resource.
type Comments struct {
	Client     *transport.Client
	Repository string
}

// List returns every discussion comment; the API does not paginate this collection.
func (s Comments) List(ctx context.Context, n int64) ([]Comment, error) {
	comments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Comment, *gitea.Response, error) {
		return c.Issues.ListIssueComments(ctx, owner, repo, n, gitea.ListIssueCommentOptions{Page: -1})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(comments, commentFrom), nil
}

// Page applies CLI pagination to the unpaginated comment collection.
func (s Comments) Page(ctx context.Context, n int64, limit, page int) ([]Comment, error) {
	items, err := s.List(ctx, n)
	if err != nil {
		return nil, err
	}
	return pageOf(items, limit, page)
}

// View retrieves a comment by its repository-scoped ID.
func (s Comments) View(ctx context.Context, id int64) (Comment, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		return c.Issues.GetIssueComment(ctx, owner, repo, id)
	})
	return item, Classify(err, "comment", strconv.FormatInt(id, 10), s.Repository)
}

// Create adds a comment to an issue or pull request.
func (s Comments) Create(ctx context.Context, n int64, body string) (Comment, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		return c.Issues.CreateIssueComment(ctx, owner, repo, n, gitea.CreateIssueCommentOption{Body: body})
	})
	return item, Classify(err, "issue", "#"+strconv.FormatInt(n, 10), s.Repository)
}

// Edit replaces the body of a repository comment.
func (s Comments) Edit(ctx context.Context, id int64, body string) (Comment, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		return c.Issues.EditIssueComment(ctx, owner, repo, id, gitea.EditIssueCommentOption{Body: body})
	})
	return item, Classify(err, "comment", strconv.FormatInt(id, 10), s.Repository)
}

// Delete removes a repository comment.
func (s Comments) Delete(ctx context.Context, id int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteIssueComment(ctx, owner, repo, id)
	})
	return Classify(err, "comment", strconv.FormatInt(id, 10), s.Repository)
}
