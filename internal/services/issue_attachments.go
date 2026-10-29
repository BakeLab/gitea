// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"strconv"

	gitea "gitea.dev/sdk"
)

// Attachments paginates the server's unpaginated attachment collection locally.
func (s Issues) Attachments(ctx context.Context, number int64, limit, page int) (Page[IssueAttachment], error) {
	attachments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.ListIssueAttachments(ctx, owner, repo, number)
	})
	if err != nil {
		return Page[IssueAttachment]{}, s.issueError(ctx, err, number)
	}
	items, err := pageOf(attachments, limit, page)
	if err != nil {
		return Page[IssueAttachment]{}, err
	}
	return Page[IssueAttachment]{
		Items: convertAll(items, func(item *gitea.Attachment) IssueAttachment { return IssueAttachment(assetFrom(item)) }),
		More:  len(attachments) > page*limit, Total: len(attachments), Counted: true,
	}, nil
}

// Attachment retrieves metadata for one attachment in its owning issue.
func (s Issues) Attachment(ctx context.Context, number, id int64) (IssueAttachment, error) {
	attachment, err := s.attachment(ctx, number, id)
	if err != nil {
		return IssueAttachment{}, err
	}
	return IssueAttachment(assetFrom(attachment)), nil
}

func (s Issues) attachment(ctx context.Context, number, id int64) (*gitea.Attachment, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.GetIssueAttachment(ctx, owner, repo, number, id)
	})
	return attachment, s.attachmentError(err, number, id)
}

// attachmentError names attachment id of issue number in a failure; Gitea answers 404 both for a
// missing issue and a missing attachment.
func (s Issues) attachmentError(err error, number, id int64) error {
	return Classify(err, "attachment", strconv.FormatInt(id, 10), fmt.Sprintf("%s#%d", s.Repository, number))
}

// RenameAttachment changes the filename without replacing its content.
func (s Issues) RenameAttachment(ctx context.Context, number, id int64, name string) (IssueAttachment, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.EditIssueAttachment(ctx, owner, repo, number, id, gitea.EditAttachmentOptions{Name: name})
	})
	if err != nil {
		return IssueAttachment{}, s.attachmentError(err, number, id)
	}
	return IssueAttachment(assetFrom(attachment)), nil
}

// DeleteAttachment removes one attachment from its owning issue.
func (s Issues) DeleteAttachment(ctx context.Context, number, id int64) error {
	return s.attachmentError(repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteIssueAttachment(ctx, owner, repo, number, id)
	}), number, id)
}

// UploadAttachment streams a file to its owning issue or pull request.
func (s Issues) UploadAttachment(ctx context.Context, number int64, name string, input io.Reader) (IssueAttachment, error) {
	asset, err := uploadAttachment(ctx, s.Client, fmt.Sprintf("%s/%d/assets", s.endpoint(), number), name, input)
	if err != nil {
		return IssueAttachment{}, s.issueError(ctx, err, number)
	}
	return IssueAttachment(asset), nil
}

// DownloadAttachment retrieves an attachment's metadata and returns a function that opens its
// content, scoped to its instance and issue. Nothing is downloaded until that function is called,
// so callers can refuse or skip the download first.
func (s Issues) DownloadAttachment(ctx context.Context, number, id int64) (IssueAttachment, func() (io.ReadCloser, error), error) {
	attachment, err := s.attachment(ctx, number, id)
	if err != nil {
		return IssueAttachment{}, nil, err
	}
	return IssueAttachment(assetFrom(attachment)), func() (io.ReadCloser, error) {
		return downloadAttachment(ctx, s.Client, attachment)
	}, nil
}
