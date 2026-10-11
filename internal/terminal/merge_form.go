// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"

	"charm.land/huh/v2"
)

// MergePullChoice is one selectable open pull request for interactive merge.
type MergePullChoice struct {
	Number      int64
	Title, Head string
}

// MergeForm collects which pull request to merge.
type MergeForm interface {
	SelectPull(context.Context, []MergePullChoice) (int64, error)
}

// SelectPull prompts for a pull request on stderr.
func (p *FormPrompter) SelectPull(ctx context.Context, choices []MergePullChoice) (int64, error) {
	if len(choices) == 0 {
		return 0, errors.New("no open pull requests match the current branch")
	}
	options := make([]huh.Option[int64], 0, len(choices))
	for _, choice := range choices {
		options = append(options, huh.NewOption(fmt.Sprintf("#%d: %s (%s)", choice.Number, choice.Title, choice.Head), choice.Number))
	}
	selected := choices[0].Number
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewSelect[int64]().Title("Select a PR to merge:").Options(options...).Value(&selected),
	)))
	return selected, err
}
