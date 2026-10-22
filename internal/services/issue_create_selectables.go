// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
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

type issueTemplateResponse struct {
	Name            string   `json:"name"`
	About           string   `json:"about"`
	Title           string   `json:"title"`
	MarkdownContent string   `json:"content"`
	Form            []any    `json:"body"`
	Labels          []string `json:"labels"`
	Ref             string   `json:"ref"`
	FileName        string   `json:"file_name"`
}

// IssueTemplates lists repository issue templates; null responses become an empty list.
func (s Issues) IssueTemplates(ctx context.Context) ([]IssueTemplate, error) {
	var response []issueTemplateResponse
	// TODO(sdk): IssueFormElementAttributes.Options is []string, so GetIssueTemplates fails on checkbox options, which are objects.
	if err := getJSON(ctx, s.Client, "repos/"+s.Repository+"/issue_templates", &response); err != nil {
		return nil, err
	}
	result := make([]IssueTemplate, 0, len(response))
	for _, r := range response {
		result = append(result, IssueTemplate{Name: r.Name, About: r.About, Title: r.Title, Content: r.MarkdownContent, Labels: r.Labels, Form: r.Form != nil, Ref: templateRef(r.Ref), FileName: r.FileName})
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
