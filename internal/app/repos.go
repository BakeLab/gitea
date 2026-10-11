// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

// repositoryArgument selects the repository from an optional positional owner/name or URL.
func (a *application) repositoryArgument(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if a.options.repo != "" {
		return clierr.Wrap(clierr.Usage, errors.New("supply the repository as an argument or --repo, not both"))
	}
	a.options.repo = args[0]
	return nil
}

func (a *application) repoCommand() *cobra.Command {
	group := &cobra.Command{Use: "repo", Short: "Work with Gitea repositories"}
	var opts services.RepositoryListOptions
	list := &cobra.Command{Use: "list", Short: "List your repositories", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := validateRepositoryKind(opts.Kind); err != nil {
			return err
		}
		scopes := 0
		for _, selected := range []bool{opts.Owner != "", opts.Starred, opts.Watched} {
			if selected {
				scopes++
			}
		}
		if scopes > 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--owner, --starred, and --watched select mutually exclusive collections"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, more, err := (services.Repositories{Client: target.client}).List(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, func(item services.Repository) []terminal.Cell {
			return []terminal.Cell{{Text: item.FullName, Link: item.URL}}
		}); err != nil {
			return err
		}
		return a.morePagesHint(cmd, more, opts.Page)
	}}
	pageFlags(list, &opts.Limit, &opts.Page)
	list.Flags().StringVar(&opts.Owner, "owner", "", "List repositories belonging to this user or organization")
	list.Flags().StringVar(&opts.Kind, "type", "", "Repository kind: fork, source, mirror")
	list.Flags().BoolVar(&opts.Starred, "starred", false, "List your starred repositories")
	list.Flags().BoolVar(&opts.Watched, "watched", false, "List your watched repositories")
	group.AddCommand(list, a.repositorySyncCommand())
	var web bool
	view := &cobra.Command{Use: "view [repository]", Short: "View the selected repository", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo)
		if err != nil {
			return err
		}
		if web {
			return a.openBrowser(cmd, item.URL)
		}
		return writeRecord(a, cmd, item, func(item services.Repository) terminal.Record {
			record := repositoryRecord(item)
			record.Lines = append([]string{terminal.SafeLine(item.Description)}, record.Lines...)
			return record
		})
	}}
	view.Flags().BoolVar(&web, "web", false, "Open the repository in a browser")
	group.AddCommand(view)
	group.AddCommand(&cobra.Command{Use: "set-default [repository]", Short: "Save the selected repository in local Git configuration", Annotations: textOutput("set-default configures Git"), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		_, err = a.git(cmd.Context(), "config", "--local", "tea.repository", target.Host+"/"+target.Repo)
		return err
	}})
	a.addRepositoryMutations(group)
	return group
}
