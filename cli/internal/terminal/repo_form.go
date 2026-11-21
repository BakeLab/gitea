// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"

	"charm.land/huh/v2"
)

// RepoFormChoices supplies the selectable options of interactive repository create.
type RepoFormChoices struct {
	// Owners are the accounts the repository can belong to, yourself first.
	Owners               []string
	Licenses, Gitignores []string
	// ValidateName and ValidateDescription check the answers as repository creation does.
	ValidateName, ValidateDescription func(string) error
}

// RepoFormValues holds interactively collected repository create fields.
type RepoFormValues struct {
	Owner, Name, Description string
	Private, AutoInit        bool
	License                  string
	Gitignores               []string
	Clone                    bool
}

// RepoForm collects interactive repository create input.
type RepoForm interface {
	CreateRepo(context.Context, RepoFormChoices) (RepoFormValues, error)
}

// CreateRepo runs the interactive repository create form on stderr, like gh repo create: the
// owner, name, description and visibility, then whether to add initial content, which license and
// .gitignore templates it uses, and whether to clone the new repository.
func (p *FormPrompter) CreateRepo(ctx context.Context, choices RepoFormChoices) (RepoFormValues, error) {
	var values RepoFormValues
	var fields []huh.Field
	if len(choices.Owners) > 1 {
		fields = append(fields, singleSelect("Owner:", safeOptions(choices.Owners), &values.Owner))
	} else if len(choices.Owners) == 1 {
		values.Owner = choices.Owners[0]
	}
	fields = append(fields,
		requiredLineField("Repository name", &values.Name, choices.ValidateName),
		lineField("Description:", &values.Description, true).Validate(func(s string) error {
			if choices.ValidateDescription == nil {
				return nil
			}
			return choices.ValidateDescription(strings.TrimSpace(s))
		}),
		huh.NewSelect[bool]().Title("Visibility:").Options(huh.NewOption("Public", false), huh.NewOption("Private", true)).Value(&values.Private),
		huh.NewConfirm().Title("Add a README, license and .gitignore?").Value(&values.AutoInit),
	)
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(fields...))); err != nil {
		return RepoFormValues{}, err
	}
	values.Name, values.Description = strings.TrimSpace(values.Name), strings.TrimSpace(values.Description)
	var content []huh.Field
	if values.AutoInit && len(choices.Licenses) > 0 {
		options := append([]huh.Option[string]{huh.NewOption("[none]", "")}, safeOptions(choices.Licenses)...)
		content = append(content, singleSelect("License:", options, &values.License))
	}
	if values.AutoInit && len(choices.Gitignores) > 0 {
		content = append(content, multiSelect(".gitignore templates:", safeOptions(choices.Gitignores), &values.Gitignores))
	}
	content = append(content, huh.NewConfirm().Title("Clone the new repository into the current directory?").Value(&values.Clone))
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(content...))); err != nil {
		return RepoFormValues{}, err
	}
	return values, nil
}

// safeOptions offers names from the server, shown as safe lines.
func safeOptions(names []string) []huh.Option[string] {
	options := make([]huh.Option[string], 0, len(names))
	for _, name := range names {
		options = append(options, huh.NewOption(SafeLine(name), name))
	}
	return options
}
