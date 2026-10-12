// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// IssueTemplateChoice seeds title/body when selected in an interactive create form.
type IssueTemplateChoice struct {
	Name, About, Title, Body string
}

// IssueMilestoneChoice identifies an open milestone for interactive selection.
type IssueMilestoneChoice struct {
	Title string
	ID    int64
}

// IssueLabelChoice identifies a repository label for interactive selection.
type IssueLabelChoice struct {
	Name string
	ID   int64
}

// BodyEditor collects a markdown body through the shared draft editor instead of
// an inline text field. Callers must finish the returned Draft.
type BodyEditor func(context.Context, string) (Draft, error)

// IssueFormChoices supplies options for an interactive issue create form.
type IssueFormChoices struct {
	Templates  []IssueTemplateChoice
	Assignees  []string
	Milestones []IssueMilestoneChoice
	Labels     []IssueLabelChoice
	CanPush    bool
	EditBody   BodyEditor
	// AskNext asks "What's next?" after the title and body, offering metadata only on request.
	AskNext bool
}

// IssueFormValues holds interactively collected issue create fields.
type IssueFormValues struct {
	Title, Body string
	Assignees   []string
	Milestone   int64
	Labels      []int64
	Deadline    *time.Time
	Draft       Draft
}

// IssueForm collects interactive issue create input.
type IssueForm interface {
	CreateIssue(context.Context, IssueFormChoices) (IssueFormValues, error)
}

// CreateIssue runs the interactive issue create form on stderr.
func (p *FormPrompter) CreateIssue(ctx context.Context, choices IssueFormChoices) (IssueFormValues, error) {
	title, body := "", ""
	if len(choices.Templates) > 0 {
		options := []huh.Option[int]{huh.NewOption("[none]", -1)}
		for i, template := range choices.Templates {
			label := template.Name
			switch {
			case template.Name != "" && template.About != "":
				label = template.Name + " — " + template.About
			case label == "":
				label = template.About
			}
			if label == "" {
				label = "template " + strconv.Itoa(i+1)
			}
			options = append(options, huh.NewOption(label, i))
		}
		selected := -1
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().Title("Issue template:").Options(options...).Value(&selected),
		))); err != nil {
			return IssueFormValues{}, err
		}
		if selected >= 0 {
			title, body = choices.Templates[selected].Title, choices.Templates[selected].Body
		}
	}
	choices.AskNext = true
	return p.collectIssueProperties(ctx, title, body, choices)
}

// issueFields holds the shared issue form inputs that create and edit bind to.
type issueFields struct {
	title, body, deadline string
	assignees             []string
	milestone             int64
	labels                []int64
}

// groups builds the content group, titled heading, and the metadata groups. The labels name no
// resource, as issues and pull requests share them.
func (f *issueFields) groups(heading string, editBody BodyEditor, canPush bool, assignees []string, milestones []IssueMilestoneChoice, labels []IssueLabelChoice) []*huh.Group {
	return append([]*huh.Group{f.content(heading, editBody)}, f.metadata(canPush, assignees, milestones, labels)...)
}

// content asks for the title and, without editBody, the body.
func (f *issueFields) content(heading string, editBody BodyEditor) *huh.Group {
	fields := []huh.Field{titleField(&f.title)}
	if editBody == nil {
		fields = append(fields, bodyField(BodyTitle, &f.body))
	}
	return huh.NewGroup(fields...).Title(heading)
}

// metadata asks for assignees, a milestone, labels and a due date, which only people who can push
// may set.
func (f *issueFields) metadata(canPush bool, assignees []string, milestones []IssueMilestoneChoice, labels []IssueLabelChoice) []*huh.Group {
	var groups []*huh.Group
	if !canPush {
		return groups
	}
	if len(assignees) > 0 {
		groups = append(groups, huh.NewGroup(multiSelect("Assignees:", huh.NewOptions(assignees...), &f.assignees)))
	}
	if len(milestones) > 0 {
		options := []huh.Option[int64]{huh.NewOption("[none]", int64(0))}
		for _, milestone := range milestones {
			options = append(options, huh.NewOption(milestone.Title, milestone.ID))
		}
		groups = append(groups, huh.NewGroup(huh.NewSelect[int64]().Title("Milestone:").Options(options...).Value(&f.milestone)))
	}
	if len(labels) > 0 {
		options := make([]huh.Option[int64], 0, len(labels))
		for _, label := range labels {
			options = append(options, huh.NewOption(label.Name, label.ID))
		}
		groups = append(groups, huh.NewGroup(multiSelect("Labels:", options, &f.labels)))
	}
	return append(groups, huh.NewGroup(dueDateInput("Due date:", &f.deadline)))
}

// editBody runs the draft editor after the form, keeping the draft on failure.
func editBody(ctx context.Context, edit BodyEditor, body string) (string, Draft, error) {
	if edit == nil {
		return body, Draft{}, nil
	}
	draft, err := edit(ctx, body)
	if err != nil {
		return body, draft, err
	}
	return draft.Body, draft, nil
}

func dueDateInput(title string, value *string) *huh.Input {
	return huh.NewInput().Title(title).Placeholder("YYYY-MM-DD").Value(value).Validate(func(s string) error {
		_, err := parseDueDate(s)
		return err
	})
}

// parseDueDate returns nil for an empty value.
func parseDueDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return nil, errors.New("use YYYY-MM-DD")
	}
	return &parsed, nil
}

// What the create form does after the title and body.
const (
	nextSubmit = iota
	nextMetadata
	nextCancel
)

// collectIssueProperties asks for the title and, without EditBody, the body, then the metadata. With
// AskNext it asks whether to submit, add metadata first, or cancel, like gh, in between. EditBody
// collects the body once the form is submitted.
func (p *FormPrompter) collectIssueProperties(ctx context.Context, title, body string, choices IssueFormChoices) (IssueFormValues, error) {
	fields := issueFields{title: title, body: body}
	metadata := fields.metadata(choices.CanPush, choices.Assignees, choices.Milestones, choices.Labels)
	groups := []*huh.Group{fields.content("", choices.EditBody)}
	next := nextMetadata
	if choices.AskNext {
		options := []huh.Option[int]{huh.NewOption("Submit", nextSubmit)}
		if len(metadata) > 0 {
			options = append(options, huh.NewOption("Add metadata", nextMetadata))
		}
		next = nextSubmit
		options = append(options, huh.NewOption("Cancel", nextCancel))
		groups = append(groups, huh.NewGroup(huh.NewSelect[int]().Title("What's next?").Options(options...).Value(&next)))
	} else {
		groups, metadata = append(groups, metadata...), nil
	}
	if err := p.runForm(ctx, huh.NewForm(groups...)); err != nil {
		return IssueFormValues{}, err
	}
	switch {
	case next == nextCancel:
		return IssueFormValues{}, ErrCanceled
	case next == nextMetadata && len(metadata) > 0:
		if err := p.runForm(ctx, huh.NewForm(metadata...)); err != nil {
			return IssueFormValues{}, err
		}
	}
	values := IssueFormValues{Title: strings.TrimSpace(fields.title), Assignees: fields.assignees, Milestone: fields.milestone, Labels: fields.labels}
	var err error
	if values.Body, values.Draft, err = editBody(ctx, choices.EditBody, fields.body); err != nil {
		return values, err
	}
	values.Deadline, err = parseDueDate(fields.deadline)
	return values, err
}
