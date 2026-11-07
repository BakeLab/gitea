// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"time"

	"charm.land/huh/v2"
)

// MilestoneFormValues holds interactively collected milestone fields.
type MilestoneFormValues struct {
	Title, Description string
	Deadline           *time.Time
	// Editor is the configured editor command, input only: with one, ctrl+e opens it in the description, except in accessible mode.
	Editor string
}

// MilestoneForm collects interactive milestone input.
type MilestoneForm interface {
	// EditMilestone asks for the fields of a milestone, starting from current, under heading, such
	// as "Editing milestone v1.0". Empty values create a milestone.
	EditMilestone(ctx context.Context, heading string, current MilestoneFormValues) (MilestoneFormValues, error)
}

// EditMilestone runs the interactive milestone form on stderr.
func (p *FormPrompter) EditMilestone(ctx context.Context, heading string, current MilestoneFormValues) (MilestoneFormValues, error) {
	title, description, deadline := current.Title, current.Description, ""
	if current.Deadline != nil {
		deadline = current.Deadline.Format(time.DateOnly)
	}
	descriptionField := bodyField("Milestone description (markdown):", &description).withEditor(current.Editor)
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		requiredLineField("Milestone title", &title),
		descriptionField,
		dueDateInput("Milestone deadline:", &deadline),
	).Title(SafeLine(heading)))); err != nil {
		return MilestoneFormValues{}, err
	}
	parsed, ok, err := parseDueDate(deadline)
	values := MilestoneFormValues{Title: titleResult(current.Title, title), Description: fieldResult(current.Description, description)}
	if ok {
		values.Deadline = &parsed
	}
	return values, err
}
