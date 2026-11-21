// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"strings"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

func (a *application) repoLicenseCommand() *cobra.Command {
	group := &cobra.Command{Use: "license", Short: "Explore the licenses Gitea offers", Long: `List the licenses that Gitea offers for new repositories and print their text. The keys are the
values of gt repo create --license.`}
	group.AddCommand(a.repoLicenseListCommand(), a.repoLicenseViewCommand())
	return group
}

func (a *application) repoLicenseListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "list", Short: "List the licenses Gitea offers", Args: argsExact(0),
		Example: `$ gt repo license list
$ gt repo license list --json key,name`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			licenses, err := (services.Templates{Client: target.client}).Licenses(cmd.Context())
			if err != nil {
				return err
			}
			return writeTable(a, cmd, licenses, listSpec[services.LicenseEntry]{
				Columns: []terminal.Column{{Header: "Key"}, {Header: "Name", Flex: true}},
				Row: func(license services.LicenseEntry) []terminal.Cell {
					return []terminal.Cell{{Text: terminal.SafeLine(license.Key), Bold: true}, {Text: terminal.SafeLine(license.Name)}}
				},
				Context: "licenses of " + target.Host, Empty: "no licenses offered",
			}, listPage{Number: 1})
		},
	}
	return jsonOutput[services.LicenseEntry](cmd)
}

func (a *application) repoLicenseViewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "view <key>", Short: "Print a license", Args: nonBlankArgs(cobra.ExactArgs(1), "key"), ValidArgsFunction: firstArg(a.completeLicenses()),
		Long: `Print the text of a license, as gt repo license list shows its key. The key is matched without
regard to case. With --json, the license is described with its name, URL and how to apply it.`,
		Example: `$ gt repo license view MIT
$ gt repo license view apache-2.0 > LICENSE
$ gt repo license view MIT --json implementation --jq .implementation`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			license, err := (services.Templates{Client: target.client}).License(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, license)
			}
			return a.writeTemplateText(cmd, license.Body)
		},
	}
	return jsonOutput[services.License](cmd)
}

func (a *application) repoGitignoreCommand() *cobra.Command {
	group := &cobra.Command{Use: "gitignore", Short: "Explore the .gitignore templates Gitea offers", Long: `List the .gitignore templates that Gitea offers for new repositories and print their content. The
names are the values of gt repo create --gitignores.`}
	group.AddCommand(a.repoGitignoreListCommand(), a.repoGitignoreViewCommand())
	return group
}

func (a *application) repoGitignoreListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "list", Short: "List the .gitignore templates Gitea offers", Args: argsExact(0),
		Example: `$ gt repo gitignore list
$ gt repo gitignore list | grep -i python`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			templates, err := (services.Templates{Client: target.client}).Gitignores(cmd.Context())
			if err != nil {
				return err
			}
			return writeTable(a, cmd, templates, listSpec[services.GitignoreEntry]{
				Columns: []terminal.Column{{Header: "Name", Flex: true}},
				Row: func(template services.GitignoreEntry) []terminal.Cell {
					return []terminal.Cell{{Text: terminal.SafeLine(template.Name)}}
				},
				Context: ".gitignore templates of " + target.Host, Empty: "no .gitignore templates offered",
			}, listPage{Number: 1})
		},
	}
	return jsonOutput[services.GitignoreEntry](cmd)
}

func (a *application) repoGitignoreViewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "view <name>", Short: "Print a .gitignore template", Args: nonBlankArgs(cobra.ExactArgs(1), "name"), ValidArgsFunction: firstArg(a.completeTemplates(services.Repositories.Gitignores)),
		Long: `Print the content of a .gitignore template, as gt repo gitignore list shows its name. The name is
matched without regard to case.`,
		Example: `$ gt repo gitignore view Go
$ gt repo gitignore view Python >> .gitignore`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			template, err := (services.Templates{Client: target.client}).Gitignore(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, template)
			}
			return a.writeTemplateText(cmd, template.Source)
		},
	}
	return jsonOutput[services.Gitignore](cmd)
}

// writeTemplateText prints the text of a template, which a file written from it needs unchanged:
// the macOS .gitignore matches a file named "Icon\r". Only a terminal gets it sanitized.
func (a *application) writeTemplateText(cmd *cobra.Command, text string) error {
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if a.deps.Streams.OutTTY {
		text = terminal.SafeText(text)
	}
	return a.writeHuman(cmd, text)
}

// completeLicenses completes license keys with their names.
func (a *application) completeLicenses() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		licenses, err := (services.Templates{Client: t.client}).Licenses(ctx)
		return describe(licenses, func(license services.LicenseEntry) (string, string) { return license.Key, license.Name }), err
	})
}
