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
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Milestone title (required):").Value(&title).Validate(huh.ValidateNotEmpty()),
		bodyField("Milestone description (markdown):", &description),
		dueDateInput("Milestone deadline:", &deadline),
	).Title(heading))); err != nil {
		return MilestoneFormValues{}, err
	}
	parsed, err := parseDueDate(deadline)
	return MilestoneFormValues{Title: strings.TrimSpace(title), Description: description, Deadline: parsed}, err
}
