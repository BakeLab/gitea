// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"
	"time"

	"charm.land/huh/v2"
)

// MilestoneFormValues holds interactively collected milestone create fields.
type MilestoneFormValues struct {
	Title, Description string
	Deadline           *time.Time
}

// MilestoneForm collects interactive milestone create input.
type MilestoneForm interface {
	CreateMilestone(context.Context) (MilestoneFormValues, error)
}

// CreateMilestone runs the interactive milestone create form on stderr.
func (p *FormPrompter) CreateMilestone(ctx context.Context) (MilestoneFormValues, error) {
	title, description, deadline := "", "", ""
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Milestone title (required):").Value(&title).Validate(huh.ValidateNotEmpty()),
		huh.NewText().Title("Milestone description (markdown):").Value(&description),
		dueDateInput("Milestone deadline:", &deadline),
	))); err != nil {
		return MilestoneFormValues{}, err
	}
	parsed, err := parseDueDate(deadline)
	return MilestoneFormValues{Title: strings.TrimSpace(title), Description: description, Deadline: parsed}, err
}
