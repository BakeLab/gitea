// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
		// Gitea answers a missing issue here with HTTP 500 "issue does not exist".
		return nil, Issues{Client: s.Client, Repository: s.Repository}.issueError(ctx, err, n)
	}
	return convertAll(comments, commentFrom), nil
}

// Page applies CLI pagination to the unpaginated comment collection.
func (s Comments) Page(ctx context.Context, n int64, limit, page int) (Page[Comment], error) {
	items, err := s.List(ctx, n)
	if err != nil {
		return Page[Comment]{}, err
	}
	window, err := pageOf(items, limit, page)
	if err != nil {
		return Page[Comment]{}, err
	}
	return Page[Comment]{Items: window, More: len(items) > page*limit, Total: len(items), Counted: true}, nil
}

// NotPlainCommentError reports a comment that the single-comment endpoint answers with no body,
// such as an inline review comment. Such comments can still be edited and deleted by ID.
type NotPlainCommentError struct {
	ID         int64
	Repository string
}

func (e NotPlainCommentError) Error() string {
	return fmt.Sprintf("comment %d in %s is not a plain comment, such as an inline review comment, which this command cannot show", e.ID, e.Repository)
}

// View retrieves a comment by its repository-scoped ID.
func (s Comments) View(ctx context.Context, id int64) (Comment, error) {
	status := 0
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		comment, resp, err := c.Issues.GetIssueComment(ctx, owner, repo, id)
		if resp != nil && resp.Response != nil {
			status = resp.StatusCode
		}
		return comment, resp, err
	})
	if status == http.StatusNoContent {
		// Gitea answers without a body for the comments that are not plain ones, such as inline review comments.
		return Comment{}, NotPlainCommentError{ID: id, Repository: s.Repository}
	}
	return item, repositoryItemError(ctx, s.Client, s.Repository, err, "comment", strconv.FormatInt(id, 10))
}

// Create adds a comment to an issue or pull request.
func (s Comments) Create(ctx context.Context, n int64, body string) (Comment, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		return c.Issues.CreateIssueComment(ctx, owner, repo, n, gitea.CreateIssueCommentOption{Body: body})
	})
	return item, Issues{Client: s.Client, Repository: s.Repository}.issueError(ctx, err, n)
}

// timelineEvent reports a comment ID that names a timeline event, such as a label or title change,
// which Gitea answers without a body on edit and with HTTP 400 on delete.
func (s Comments) timelineEvent(id int64, verb string) error {
	return fmt.Errorf("comment %d in %s is a timeline event, such as a label or title change, which cannot be %s", id, s.Repository, verb)
}

// Edit replaces the body of a repository comment.
func (s Comments) Edit(ctx context.Context, id int64, body string) (Comment, error) {
	status := 0
	item, err := repoConvert(ctx, s.Client, s.Repository, commentFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Comment, *gitea.Response, error) {
		comment, resp, err := c.Issues.EditIssueComment(ctx, owner, repo, id, gitea.EditIssueCommentOption{Body: body})
		if resp != nil && resp.Response != nil {
			status = resp.StatusCode
		}
		return comment, resp, err
	})
	if status == http.StatusNoContent {
		return Comment{}, s.timelineEvent(id, "edited")
	}
	return item, repositoryItemError(ctx, s.Client, s.Repository, err, "comment", strconv.FormatInt(id, 10))
}

// UploadAttachment streams a file to comment id.
func (s Comments) UploadAttachment(ctx context.Context, id int64, name string, input io.Reader) (IssueAttachment, error) {
	asset, err := uploadAttachment(ctx, s.Client, fmt.Sprintf("repos/%s/issues/comments/%d/assets", s.Repository, id), name, input)
	return IssueAttachment(asset), err
}

// Attachments paginates the unpaginated attachments of comment id locally.
func (s Comments) Attachments(ctx context.Context, id int64, limit, page int) (Page[IssueAttachment], error) {
	attachments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.ListIssueCommentAttachments(ctx, owner, repo, id)
	})
	if err != nil {
		return Page[IssueAttachment]{}, repositoryItemError(ctx, s.Client, s.Repository, err, "comment", strconv.FormatInt(id, 10))
	}
	return attachmentPage(attachments, limit, page)
}

// Attachment retrieves metadata for one attachment of comment id.
func (s Comments) Attachment(ctx context.Context, id, attachmentID int64) (IssueAttachment, error) {
	attachment, err := s.attachment(ctx, id, attachmentID)
	if err != nil {
		return IssueAttachment{}, err
	}
	return IssueAttachment(assetFrom(attachment)), nil
}

func (s Comments) attachment(ctx context.Context, id, attachmentID int64) (*gitea.Attachment, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.GetIssueCommentAttachment(ctx, owner, repo, id, attachmentID)
	})
	return attachment, s.attachmentError(err, id, attachmentID)
}

// attachmentError names the attachment of comment id in a failure; Gitea answers 404 both for a
// missing comment and a missing attachment.
func (s Comments) attachmentError(err error, id, attachmentID int64) error {
	return Classify(err, "attachment", strconv.FormatInt(attachmentID, 10), fmt.Sprintf("comment %d on %s", id, s.Repository))
}

// RenameAttachment changes the filename of an attachment of comment id without replacing its content.
func (s Comments) RenameAttachment(ctx context.Context, id, attachmentID int64, name string) (IssueAttachment, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.EditIssueCommentAttachment(ctx, owner, repo, id, attachmentID, gitea.EditAttachmentOptions{Name: name})
	})
	if err != nil {
		return IssueAttachment{}, s.attachmentError(err, id, attachmentID)
	}
	return IssueAttachment(assetFrom(attachment)), nil
}

// DeleteAttachment removes one attachment from comment id.
func (s Comments) DeleteAttachment(ctx context.Context, id, attachmentID int64) error {
	return s.attachmentError(repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteIssueCommentAttachment(ctx, owner, repo, id, attachmentID)
	}), id, attachmentID)
}

// DownloadAttachment retrieves the metadata of an attachment of comment id and returns a function
// that opens its content, as Issues.DownloadAttachment does.
func (s Comments) DownloadAttachment(ctx context.Context, id, attachmentID int64) (IssueAttachment, func() (io.ReadCloser, error), error) {
	attachment, err := s.attachment(ctx, id, attachmentID)
	if err != nil {
		return IssueAttachment{}, nil, err
	}
	return IssueAttachment(assetFrom(attachment)), func() (io.ReadCloser, error) {
		return downloadAttachment(ctx, s.Client, attachment)
	}, nil
}

// Delete removes a repository comment.
func (s Comments) Delete(ctx context.Context, id int64) error {
	status := 0
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		resp, err := c.Issues.DeleteIssueComment(ctx, owner, repo, id)
		if resp != nil && resp.Response != nil {
			status = resp.StatusCode
		}
		return resp, err
	})
	if status == http.StatusBadRequest {
		return s.timelineEvent(id, "deleted")
	}
	return repositoryItemError(ctx, s.Client, s.Repository, err, "comment", strconv.FormatInt(id, 10))
}
