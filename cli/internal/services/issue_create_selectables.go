// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"

	gitea "gitea.dev/sdk"
)

// IssueTemplate is a repository issue template used to seed create forms.
type IssueTemplate struct {
	Name, About, Title, Content string
	Labels                      []string
	Form                        bool
	// Ref is the branch or tag the template sets on new issues.
	Ref string
	// FileName is the path of the template in the repository, which the new-issue page takes.
	FileName string
}

// IssueTemplates lists repository issue templates; null responses become an empty list.
func (s Issues) IssueTemplates(ctx context.Context) ([]IssueTemplate, error) {
	response, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.IssueTemplate, *gitea.Response, error) {
		return c.Issues.GetIssueTemplates(ctx, owner, repo)
	})
	if err != nil {
		return nil, err
	}
	result := make([]IssueTemplate, 0, len(response))
	for _, r := range response {
		result = append(result, IssueTemplate{Name: r.Name, About: r.About, Title: r.IssueTitle, Content: r.MarkdownContent, Labels: r.IssueLabels, Form: r.IsForm(), Ref: templateRef(r.IssueRef), FileName: r.Filename})
	}
	return result, nil
}

// templateRef drops the bare prefix Gitea reports for a template without a ref.
func templateRef(ref string) string {
	if ref == "refs/heads/" || ref == "refs/tags/" {
		return ""
	}
	return ref
}

// IssueCreateSelectables adds repository issue templates to the create form choices.
type IssueCreateSelectables struct {
	CreateSelectables
	Templates []IssueTemplate
}

// CreateSelectables concurrently loads templates, assignees, milestones and labels. The metadata
// choices do not depend on push access, which is all the permissions of a repository tell: people
// who can write to the issues set metadata without it, and issue create names what Gitea left out
// for others. CanPush stays false.
func (s Issues) CreateSelectables(ctx context.Context) (IssueCreateSelectables, error) {
	var result IssueCreateSelectables
	err := loadTogether(
		func() (err error) {
			result.CreateSelectables, err = loadMetadataChoices(ctx, s.Client, s.Repository)
			return err
		},
		func() (err error) {
			result.Templates, err = s.IssueTemplates(ctx)
			return err
		},
	)
	if err != nil {
		return IssueCreateSelectables{}, err
	}
	return result, nil
}

// EditSelectables loads the assignees, milestones and labels the edit form offers. Like those of
// CreateSelectables, they do not depend on push access, and issue edit names what Gitea left out
// for people who cannot write to the issues. CanPush stays false.
func (s Issues) EditSelectables(ctx context.Context) (CreateSelectables, error) {
	return loadMetadataChoices(ctx, s.Client, s.Repository)
}
