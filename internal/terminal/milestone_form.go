// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// MilestoneFormValues holds interactively collected milestone fields.
type MilestoneFormValues struct {
	Title, Description string
	Deadline           *time.Time
	// Editor is the configured editor command, input only: with one, ctrl+e opens it in the description.
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
	descriptionTitle := "Milestone description (markdown):"
	descriptionField := bodyField(descriptionTitle, &description)
	if args, ok := commandArgs(current.Editor); ok {
		descriptionField = bodyField(strings.TrimSuffix(descriptionTitle, "):")+", ctrl+e opens your editor):", &description).ExternalEditor(true).Editor(args...).EditorExtension("md")
	}
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Milestone title (required):").Value(&title).Validate(notBlank),
		descriptionField,
		dueDateInput("Milestone deadline:", &deadline),
	).Title(SafeLine(heading)))); err != nil {
		return MilestoneFormValues{}, err
	}
	parsed, err := parseDueDate(deadline)
	return MilestoneFormValues{Title: strings.TrimSpace(title), Description: fieldResult(current.Description, description), Deadline: parsed}, err
}
