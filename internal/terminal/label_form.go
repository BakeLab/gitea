// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"charm.land/huh/v2"
)

// LabelFormValues holds interactively edited label fields.
type LabelFormValues struct {
	Name, Color, Description string
	Exclusive, Archived      bool
}

// LabelForm collects interactive label input.
type LabelForm interface {
	// EditLabel asks for the fields of a label, starting from current, under heading, such as
	// `Editing label "bug" in alice/x`.
	EditLabel(ctx context.Context, heading string, current LabelFormValues) (LabelFormValues, error)
}

var hexColor = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

// EditLabel runs the interactive label form on stderr.
func (p *FormPrompter) EditLabel(ctx context.Context, heading string, current LabelFormValues) (LabelFormValues, error) {
	values := current
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Label name (required):").Value(&values.Name).Validate(huh.ValidateNotEmpty()),
		huh.NewInput().Title("Color (hex, e.g. d73a4a):").Value(&values.Color).Validate(func(s string) error {
			if !hexColor.MatchString(strings.TrimSpace(s)) {
				return errors.New("use six hexadecimal digits")
			}
			return nil
		}),
		huh.NewInput().Title("Description (optional):").Value(&values.Description),
		huh.NewConfirm().Title("Exclusive (one label of its scope per issue):").Value(&values.Exclusive),
		huh.NewConfirm().Title("Archived:").Value(&values.Archived),
	).Title(heading))); err != nil {
		return LabelFormValues{}, err
	}
	values.Name, values.Color = strings.TrimSpace(values.Name), strings.TrimPrefix(strings.TrimSpace(values.Color), "#")
	return values, nil
}
