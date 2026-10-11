// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/huh/v2"
)

// PullFormChoices supplies defaults and selectable options for interactive PR create.
type PullFormChoices struct {
	BaseOwner, Base, HeadOwner, HeadBranch string
	// Title and Body seed the form; Title applies only while HeadBranch is kept.
	Title, Body          string
	AllowMaintainerEdits bool
	Assignees            []string
	Milestones           []IssueMilestoneChoice
	Labels               []IssueLabelChoice
	CanPush              bool
	EditBody             BodyEditor
}

// PullFormValues holds interactively collected pull request create fields.
type PullFormValues struct {
	IssueFormValues
	Agit                 bool
	Base, Head, Topic    string
	AllowMaintainerEdits bool
}

// PullForm collects interactive pull create input.
type PullForm interface {
	CreatePull(context.Context, PullFormChoices) (PullFormValues, error)
}

// CreatePull runs the interactive pull create form on stderr.
func (p *FormPrompter) CreatePull(ctx context.Context, choices PullFormChoices) (PullFormValues, error) {
	agit, err := p.Confirm(ctx, "Do you want to create an agit flow pull request?")
	if err != nil {
		return PullFormValues{}, err
	}

	base := choices.Base
	headOwner := choices.HeadOwner
	headBranch := choices.HeadBranch
	topic := headBranch
	allowMaintainerEdits := choices.AllowMaintainerEdits

	fields := []huh.Field{
		huh.NewInput().Title("Target branch (required):").Value(&base).Validate(huh.ValidateNotEmpty()),
	}
	if agit {
		fields = append(fields, huh.NewInput().Title("Topic branch (required):").Value(&topic).Validate(huh.ValidateNotEmpty()))
	} else {
		headField := huh.NewInput().Title("Source branch:").Value(&headBranch)
		if strings.TrimSpace(headBranch) == "" {
			headField = headField.Title("Source branch (required):").Validate(huh.ValidateNotEmpty())
		}
		fields = append(fields,
			huh.NewInput().Title("Source repo owner:").Value(&headOwner),
			headField,
			huh.NewConfirm().Title("Allow maintainers to push to the source branch:").Value(&allowMaintainerEdits),
		)
	}
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(fields...))); err != nil {
		return PullFormValues{}, err
	}

	title := DefaultPullTitle(headBranch)
	if choices.Title != "" && headBranch == choices.HeadBranch {
		title = choices.Title
	}
	// AGit pushes carry only a title and description.
	properties, err := p.collectIssueProperties(ctx, title, choices.Body, IssueFormChoices{
		Assignees: choices.Assignees, Milestones: choices.Milestones, Labels: choices.Labels, CanPush: choices.CanPush && !agit,
		EditBody: choices.EditBody,
	})
	if err != nil {
		return PullFormValues{IssueFormValues: properties}, err
	}

	headBranch = strings.TrimSpace(headBranch)
	headOwner = strings.TrimSpace(headOwner)
	head := headBranch
	switch {
	case agit && head == "":
		head = "HEAD"
	case !agit && headOwner != "" && headOwner != choices.BaseOwner:
		head = headOwner + ":" + headBranch
	}
	return PullFormValues{
		IssueFormValues:      properties,
		Agit:                 agit,
		Base:                 strings.TrimSpace(base),
		Head:                 head,
		Topic:                strings.TrimSpace(topic),
		AllowMaintainerEdits: allowMaintainerEdits,
	}, nil
}

// DefaultPullTitle turns a branch-like head into a readable pull title seed.
func DefaultPullTitle(head string) string {
	if i := strings.LastIndex(head, ":"); i != -1 {
		head = head[i+1:]
	}
	parts := strings.Fields(strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(head)))
	if len(parts) == 0 {
		return ""
	}
	for i, part := range parts {
		r, size := utf8.DecodeRuneInString(part)
		parts[i] = string(unicode.ToUpper(r)) + part[size:]
	}
	return strings.Join(parts, " ")
}
