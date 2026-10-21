// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// IssueEditFormChoices supplies current issue values and selectable options for edit.
// Current values missing from the options stay selectable.
type IssueEditFormChoices struct {
	// Heading names the edited issue above the form, such as "Editing alice/x#2 (Fix login)".
	Heading            string
	Title, Body        string
	Assignees          []string
	Milestone          *IssueMilestoneChoice
	Labels             []IssueLabelChoice
	Deadline           *time.Time
	AvailableAssignees []string
	Milestones         []IssueMilestoneChoice
	AvailableLabels    []IssueLabelChoice
	EditBody           BodyEditor
}

// IssueEditFormValues holds interactively edited issue fields. Deadline is set
// only when the due date changed.
type IssueEditFormValues struct {
	Title, Body    string
	Assignees      []string
	Milestone      int64
	ClearMilestone bool
	Labels         []int64
	Deadline       *time.Time
	ClearDeadline  bool
	Draft          Draft
}

// IssueEditForm collects interactive issue edit input.
type IssueEditForm interface {
	EditIssue(context.Context, IssueEditFormChoices) (IssueEditFormValues, error)
}

// EditIssue runs the interactive issue edit form on stderr, seeded with the current values.
func (p *FormPrompter) EditIssue(ctx context.Context, choices IssueEditFormChoices) (IssueEditFormValues, error) {
	deadline := ""
	if choices.Deadline != nil && !choices.Deadline.IsZero() {
		// Keep the server's offset: converting to UTC can move the date by a day.
		deadline = choices.Deadline.Format(time.DateOnly)
	}
	fields := issueFields{title: choices.Title, body: choices.Body, deadline: deadline, assignees: choices.Assignees}
	milestones := choices.Milestones
	if choices.Milestone != nil {
		fields.milestone, milestones = choices.Milestone.ID, withCurrent(milestones, *choices.Milestone)
	}
	for _, label := range choices.Labels {
		fields.labels = append(fields.labels, label.ID)
	}
	groups := fields.groups(choices.Heading, choices.EditBody, withCurrent(choices.AvailableAssignees, choices.Assignees...), milestones, withCurrent(choices.AvailableLabels, choices.Labels...))
	if err := p.runForm(ctx, huh.NewForm(groups...)); err != nil {
		return IssueEditFormValues{}, err
	}
	values := IssueEditFormValues{Title: titleResult(choices.Title, fields.title), Assignees: fields.assignees, Milestone: fields.milestone, Labels: fields.labels}
	values.ClearMilestone = fields.milestone == 0 && choices.Milestone != nil
	if choices.EditBody == nil {
		fields.body = fieldResult(choices.Body, fields.body)
	}
	var err error
	if values.Body, values.Draft, err = editBody(ctx, choices.EditBody, fields.body); err != nil {
		return values, err
	}
	if strings.TrimSpace(fields.deadline) == deadline {
		return values, nil
	}
	values.Deadline, err = parseDueDate(fields.deadline)
	values.ClearDeadline = values.Deadline == nil && deadline != ""
	return values, err
}

// withCurrent appends current values missing from options; selects drop values they do not offer.
func withCurrent[T comparable](options []T, current ...T) []T {
	for _, value := range current {
		if !slices.Contains(options, value) {
			options = append(slices.Clip(options), value)
		}
	}
	return options
}
