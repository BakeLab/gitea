// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"

	gitea "gitea.dev/sdk"
)

// Attachments paginates the server's unpaginated attachment collection locally.
func (s Issues) Attachments(ctx context.Context, number int64, limit, page int) ([]IssueAttachment, error) {
	attachments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.ListIssueAttachments(ctx, owner, repo, number)
	})
	if err != nil {
		return nil, err
	}
	items, err := pageOf(attachments, limit, page)
	if err != nil {
		return nil, err
	}
	return convertAll(items, func(item *gitea.Attachment) IssueAttachment { return IssueAttachment(assetFrom(item)) }), nil
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
	return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.GetIssueAttachment(ctx, owner, repo, number, id)
	})
}

// RenameAttachment changes the filename without replacing its content.
func (s Issues) RenameAttachment(ctx context.Context, number, id int64, name string) (IssueAttachment, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Issues.EditIssueAttachment(ctx, owner, repo, number, id, gitea.EditAttachmentOptions{Name: name})
	})
	if err != nil {
		return IssueAttachment{}, err
	}
	return IssueAttachment(assetFrom(attachment)), nil
}

// DeleteAttachment removes one attachment from its owning issue.
func (s Issues) DeleteAttachment(ctx context.Context, number, id int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteIssueAttachment(ctx, owner, repo, number, id)
	})
}

// UploadAttachment streams a file to its owning issue or pull request.
func (s Issues) UploadAttachment(ctx context.Context, number int64, name string, input io.Reader) (IssueAttachment, error) {
	asset, err := uploadAttachment(ctx, s.Client, fmt.Sprintf("%s/%d/assets", s.endpoint(), number), name, input)
	return IssueAttachment(asset), err
}

// DownloadAttachment opens an attachment stream scoped to its instance and issue.
func (s Issues) DownloadAttachment(ctx context.Context, number, id int64) (io.ReadCloser, error) {
	attachment, err := s.attachment(ctx, number, id)
	if err != nil {
		return nil, err
	}
	return downloadAttachment(ctx, s.Client, attachment)
}
