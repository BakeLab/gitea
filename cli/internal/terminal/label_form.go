// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"cmp"
	"context"
	"strings"

	"charm.land/huh/v2"
)

// LabelFormValues holds interactively edited label fields.
type LabelFormValues struct {
	Name, Color, Description string
}

// LabelForm collects interactive label input.
type LabelForm interface {
	// EditLabel asks for the name, color and description of a label, starting from current, under
	// heading, such as `Editing label "bug" in alice/x`. validColor checks a color answer.
	EditLabel(ctx context.Context, heading string, current LabelFormValues, validColor func(string) error) (LabelFormValues, error)
}

// EditLabel runs the interactive label form on stderr.
func (p *FormPrompter) EditLabel(ctx context.Context, heading string, current LabelFormValues, validColor func(string) error) (LabelFormValues, error) {
	name, color, description := current.Name, current.Color, current.Description
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		requiredLineField("Label name", &name),
		lineField("Color (hex, e.g. d73a4a):", &color, false).Validate(func(s string) error {
			if err := requiredText(&color)(s); err != nil {
				return err
			}
			// An accessible form checks the typed line, which is empty when it keeps the color.
			return validColor(strings.TrimSpace(cmp.Or(s, color)))
		}),
		lineField("Description (optional):", &description, true),
	).Title(SafeLine(heading)))); err != nil {
		return LabelFormValues{}, err
	}
	return LabelFormValues{Name: titleResult(current.Name, name), Color: titleResult(current.Color, color), Description: titleResult(current.Description, description)}, nil
}
