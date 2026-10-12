// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"

	"charm.land/huh/v2"
)

// Choice is one option of a selection prompt: Label is shown and Value returned.
type Choice struct {
	Label, Value string
}

// Chooser asks for one of several options. FormPrompter implements it; a Prompter without it
// cannot offer a selection.
type Chooser interface {
	// Choose asks for one of choices, starting at the one whose value is initial, else the first.
	Choose(ctx context.Context, title string, choices []Choice, initial string) (string, error)
}

// Choose asks on stderr for one of choices and returns its value.
func (p *FormPrompter) Choose(ctx context.Context, title string, choices []Choice, initial string) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("nothing to choose from")
	}
	options := make([]huh.Option[string], len(choices))
	value := choices[0].Value
	for i, choice := range choices {
		options[i] = huh.NewOption(SafeLine(choice.Label), choice.Value)
		if choice.Value == initial {
			value = initial
		}
	}
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title(title).Options(options...).Height(min(len(choices), 10)+2).Value(&value))))
	return value, err
}
