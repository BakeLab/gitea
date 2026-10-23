// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"

	"charm.land/huh/v2"
)

// ReleaseFormValues holds interactively collected release fields.
type ReleaseFormValues struct {
	Tag, Title, Notes, Target string
	Draft, Prerelease         bool
	// Editor is the configured editor command, input only: with one, ctrl+e opens it in the notes, except in accessible mode.
	Editor string
}

// ReleaseForm collects interactive release input.
type ReleaseForm interface {
	// EditRelease asks for the fields of a release, starting from current, under heading, such as
	// "Editing release v1.0 in alice/x". Empty values create a release.
	EditRelease(ctx context.Context, heading string, current ReleaseFormValues) (ReleaseFormValues, error)
}

// EditRelease runs the interactive release form on stderr.
func (p *FormPrompter) EditRelease(ctx context.Context, heading string, current ReleaseFormValues) (ReleaseFormValues, error) {
	values := current
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		requiredLineField("Tag", &values.Tag),
		lineField("Title (empty uses the tag):", &values.Title, true),
		bodyField("Release notes (markdown):", &values.Notes).withEditor(current.Editor),
		lineField("Target branch or commit, for a new tag (empty uses the default branch):", &values.Target, true),
		huh.NewConfirm().Title("Save as a draft?").Value(&values.Draft),
		huh.NewConfirm().Title("Mark as a prerelease?").Value(&values.Prerelease),
	).Title(SafeLine(heading)))); err != nil {
		return ReleaseFormValues{}, err
	}
	values.Tag, values.Title, values.Target = titleResult(current.Tag, values.Tag), titleResult(current.Title, values.Title), titleResult(current.Target, values.Target)
	values.Notes = fieldResult(current.Notes, values.Notes)
	return values, nil
}
