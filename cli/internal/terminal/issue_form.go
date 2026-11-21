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

// IssueTemplateChoice seeds title/body when selected in an interactive create form. Its labels and
// ref are returned as IssueFormValues.TemplateLabels and TemplateRef for the caller to apply.
type IssueTemplateChoice struct {
	Name, About, Title, Body, Ref string
	Labels                        []string
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
	Templates []IssueTemplateChoice
	// Template, when set, is used instead of asking: the form starts with its title and body.
	Template   *IssueTemplateChoice
	Assignees  []string
	Milestones []IssueMilestoneChoice
	Labels     []IssueLabelChoice
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
	// TemplateLabels are the label names of the chosen template, which the caller adds to Labels.
	TemplateLabels []string
	// TemplateRef is the branch or tag the chosen template sets on the issue.
	TemplateRef string
}

// IssueForm collects interactive issue create input.
type IssueForm interface {
	CreateIssue(context.Context, IssueFormChoices) (IssueFormValues, error)
}

// CreateIssue runs the interactive issue create form on stderr.
func (p *FormPrompter) CreateIssue(ctx context.Context, choices IssueFormChoices) (IssueFormValues, error) {
	seed := &IssueTemplateChoice{}
	if choices.Template != nil {
		seed = choices.Template
	} else if len(choices.Templates) > 0 {
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
			options = append(options, huh.NewOption(SafeLine(label), i))
		}
		selected := -1
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().Title("Issue template:").Options(options...).Height(min(len(options), 10)+2).Value(&selected),
		))); err != nil {
			return IssueFormValues{}, err
		}
		if selected >= 0 {
			seed = &choices.Templates[selected]
		}
	}
	choices.AskNext = true
	values, err := p.collectIssueProperties(ctx, seed.Title, seed.Body, choices)
	values.TemplateLabels, values.TemplateRef = seed.Labels, seed.Ref
	return values, err
}

// issueFields holds the shared issue form inputs that create and edit bind to.
type issueFields struct {
	title, body, deadline string
	assignees             []string
	milestone             int64
	labels                []int64
	// base, when set, is the base branch of an edited pull request.
	base *string
}

// groups builds the content group, titled heading, and the metadata groups. The labels name no
// resource, as issues and pull requests share them.
func (f *issueFields) groups(heading string, editBody BodyEditor, assignees []string, milestones []IssueMilestoneChoice, labels []IssueLabelChoice) []*huh.Group {
	return append([]*huh.Group{f.content(heading, editBody)}, f.metadata(assignees, milestones, labels)...)
}

// content asks for the title, the base branch when set and, without editBody, the body.
func (f *issueFields) content(heading string, editBody BodyEditor) *huh.Group {
	fields := []huh.Field{titleField(&f.title)}
	if f.base != nil {
		fields = append(fields, requiredLineField("Base branch", f.base))
	}
	if editBody == nil {
		fields = append(fields, bodyField(BodyTitle, &f.body))
	}
	return huh.NewGroup(fields...).Title(SafeLine(heading))
}

// bodyResult is the body after the form: with the inline field of content, what fieldResult makes
// of the answer to a field seeded with seed.
func (f *issueFields) bodyResult(seed string, editBody BodyEditor) string {
	if editBody != nil {
		return f.body
	}
	return fieldResult(seed, f.body)
}

// metadata asks for assignees, a milestone, labels and a due date. They need no push access:
// people who can write to the issues set them too.
func (f *issueFields) metadata(assignees []string, milestones []IssueMilestoneChoice, labels []IssueLabelChoice) []*huh.Group {
	var groups []*huh.Group
	if len(assignees) > 0 {
		groups = append(groups, huh.NewGroup(multiSelect("Assignees:", huh.NewOptions(assignees...), &f.assignees)))
	}
	if len(milestones) > 0 {
		options := []huh.Option[int64]{huh.NewOption("[none]", int64(0))}
		for _, milestone := range milestones {
			options = append(options, huh.NewOption(SafeLine(milestone.Title), milestone.ID))
		}
		groups = append(groups, huh.NewGroup(huh.NewSelect[int64]().Title("Milestone:").Options(options...).Height(min(len(options), 10)+2).Value(&f.milestone)))
	}
	if len(labels) > 0 {
		options := make([]huh.Option[int64], 0, len(labels))
		for _, label := range labels {
			options = append(options, huh.NewOption(SafeLine(label.Name), label.ID))
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

// dueDateInput asks for a date. Its character limit, a date's length, also keeps the input at
// least zero columns wide on a terminal narrower than its frame, where the placeholder would
// otherwise panic.
func dueDateInput(title string, value *string) *lineInput {
	field := lineField(title, value, true).Validate(func(s string) error {
		_, _, err := parseDueDate(s)
		return err
	})
	field.Placeholder("YYYY-MM-DD").CharLimit(len(time.DateOnly))
	return field
}

func parseDueDate(value string) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return time.Time{}, false, errors.New("use YYYY-MM-DD")
	}
	return parsed, true, nil
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
	metadata := fields.metadata(choices.Assignees, choices.Milestones, choices.Labels)
	content := fields.content("", choices.EditBody)
	values := IssueFormValues{}
	var err error
	if !choices.AskNext {
		if err := p.runForm(ctx, huh.NewForm(append([]*huh.Group{content}, metadata...)...)); err != nil {
			return IssueFormValues{}, err
		}
		fields.body = fields.bodyResult(body, choices.EditBody)
	} else {
		if err := p.runForm(ctx, huh.NewForm(content)); err != nil {
			return IssueFormValues{}, err
		}
		fields.body = fields.bodyResult(body, choices.EditBody)
		// The body comes before "What's next?", so canceling still reports the draft.
		if values.Body, values.Draft, err = editBody(ctx, choices.EditBody, fields.body); err != nil {
			return values, err
		}
		options := []huh.Option[int]{huh.NewOption("Submit", nextSubmit)}
		if len(metadata) > 0 {
			options = append(options, huh.NewOption("Add metadata", nextMetadata))
		}
		options = append(options, huh.NewOption("Cancel", nextCancel))
		for next := nextMetadata; next == nextMetadata; {
			next = nextSubmit
			if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewSelect[int]().Title("What's next?").Options(options...).Value(&next)))); err != nil {
				return values, err
			}
			switch next {
			case nextCancel:
				return values, ErrCanceled
			case nextMetadata:
				if err := p.runForm(ctx, huh.NewForm(metadata...)); err != nil {
					return values, err
				}
			}
		}
	}
	values.Title, values.Assignees, values.Milestone, values.Labels = strings.TrimSpace(fields.title), fields.assignees, fields.milestone, fields.labels
	if !choices.AskNext {
		if values.Body, values.Draft, err = editBody(ctx, choices.EditBody, fields.body); err != nil {
			return values, err
		}
	}
	deadline, ok, err := parseDueDate(fields.deadline)
	if ok {
		values.Deadline = &deadline
	}
	return values, err
}
