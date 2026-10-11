// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
)

// IssueTemplate is a repository issue template used to seed create forms.
type IssueTemplate struct {
	Name, About, Title, Content string
	Form                        bool
}

type issueTemplateResponse struct {
	Name            string `json:"name"`
	About           string `json:"about"`
	Title           string `json:"title"`
	MarkdownContent string `json:"content"`
	Form            []any  `json:"body"`
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
		result = append(result, IssueTemplate{Name: r.Name, About: r.About, Title: r.Title, Content: r.MarkdownContent, Form: r.Form != nil})
	}
	return result, nil
}

// IssueCreateSelectables adds repository issue templates to the create form choices.
type IssueCreateSelectables struct {
	CreateSelectables
	Templates []IssueTemplate
}

// CreateSelectables loads templates and, when the user can push, assignees/milestones/labels.
func (s Issues) CreateSelectables(ctx context.Context) (IssueCreateSelectables, error) {
	choices, err := loadCreateSelectables(ctx, s.Client, s.Repository)
	if err != nil {
		return IssueCreateSelectables{}, err
	}
	templates, err := s.IssueTemplates(ctx)
	return IssueCreateSelectables{CreateSelectables: choices, Templates: templates}, err
}
