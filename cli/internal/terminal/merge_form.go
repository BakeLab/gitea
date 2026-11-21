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

// MergeMethodLabels describe Gitea's merge styles in the merge form.
var MergeMethodLabels = map[string]string{
	"merge": "Create a merge commit", "rebase": "Rebase and merge", "rebase-merge": "Rebase and create a merge commit",
	"squash": "Squash and merge", "fast-forward-only": "Fast-forward only",
}

// MergeFormChoices describes the pull request to merge and what the form asks about it.
type MergeFormChoices struct {
	// Header introduces the form, such as `Merge #3 "Merge me" (feat/merge-me → main) in alice/x`.
	Header string
	// Methods are the merge styles the repository allows; Method preselects one. Without Methods,
	// the form does not ask for one.
	Methods []string
	Method  string
	// DeleteBranch preselects whether to delete the head branch; Branch, when set, asks it.
	DeleteBranch bool
	Branch       string
}

// MergeFormValues are the chosen merge style and whether to delete the head branch.
type MergeFormValues struct {
	Method       string
	DeleteBranch bool
}

// MergeForm collects which pull request to merge, and how.
type MergeForm interface {
	SelectPull(context.Context, []MergePullChoice) (int64, error)
	MergeOptions(context.Context, MergeFormChoices) (MergeFormValues, error)
}

// SelectPull prompts for a pull request on stderr.
func (p *FormPrompter) SelectPull(ctx context.Context, choices []MergePullChoice) (int64, error) {
	if len(choices) == 0 {
		return 0, errors.New("no open pull requests match the current branch")
	}
	options := make([]huh.Option[int64], 0, len(choices))
	for _, choice := range choices {
		options = append(options, huh.NewOption(SafeLine(fmt.Sprintf("#%d %s (%s)", choice.Number, choice.Title, choice.Head)), choice.Number))
	}
	selected := choices[0].Number
	err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewSelect[int64]().Title("Pull request to merge:").Options(options...).Height(min(len(options), 10)+2).Value(&selected),
	)))
	return selected, err
}

// MergeOptions asks for the merge style and whether to delete the head branch, on stderr.
func (p *FormPrompter) MergeOptions(ctx context.Context, choices MergeFormChoices) (MergeFormValues, error) {
	values := MergeFormValues{Method: choices.Method, DeleteBranch: choices.DeleteBranch}
	var fields []huh.Field
	if len(choices.Methods) > 0 {
		options := make([]huh.Option[string], len(choices.Methods))
		for i, method := range choices.Methods {
			options[i] = huh.NewOption(MergeMethodLabels[method], method)
		}
		fields = append(fields, huh.NewSelect[string]().Title("Merge method:").Options(options...).Value(&values.Method))
	}
	if choices.Branch != "" {
		fields = append(fields, huh.NewConfirm().Title(SafeLine(fmt.Sprintf("Delete branch %s locally and on Gitea?", choices.Branch))).Value(&values.DeleteBranch))
	}
	if choices.Header != "" {
		fmt.Fprintln(p.Streams.ErrOut, SafeLine(choices.Header))
	}
	if len(fields) == 0 {
		return values, nil
	}
	return values, p.runForm(ctx, huh.NewForm(huh.NewGroup(fields...)))
}
