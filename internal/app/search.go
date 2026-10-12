// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func validateRepositoryKind(kind string) error {
	switch kind {
	case "", "fork", "source", "mirror":
		return nil
	default:
		return clierr.Wrap(clierr.Usage, errors.New("--type must be fork, source, or mirror"))
	}
}

func (a *application) searchCommand() *cobra.Command {
	group := &cobra.Command{Use: "search", Short: "Search within one selected Gitea instance"}
	var opts services.RepositorySearchOptions
	var private, archived bool
	command := &cobra.Command{Use: "repo [query]...", Short: "Search repository names, descriptions or topics", RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateRepositoryKind(opts.Kind); err != nil {
			return err
		}
		if err := oneOf("sort", opts.Sort, "alpha", "created", "updated", "size", "id"); err != nil {
			return err
		}
		if err := oneOf("order", opts.Order, "asc", "desc"); err != nil {
			return err
		}
		if cmd.Flags().Changed("private") {
			opts.Private = &private
		}
		if cmd.Flags().Changed("archived") {
			opts.Archived = &archived
		}
		opts.Query = strings.Join(args, " ")
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, err := (services.Repositories{Client: target.client}).Search(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, func(item services.Repository) []terminal.Cell {
			return terminal.Row(item.FullName, item.Description, item.URL)
		})
	}}
	flags := command.Flags()
	pageFlags(command, &opts.Limit, &opts.Page)
	flags.StringVar(&opts.Owner, "owner", "", "Repository owner")
	flags.StringVar(&opts.Kind, "type", "", "Repository kind: fork, source, mirror")
	flags.StringVar(&opts.Sort, "sort", "alpha", "Sort by alpha, created, updated, size, id")
	flags.StringVar(&opts.Order, "order", "asc", "Sort order: asc, desc")
	flags.BoolVar(&opts.Topic, "topic", false, "Match repository topics")
	flags.BoolVar(&private, "private", false, "Only private repositories (use =false for public)")
	flags.BoolVar(&archived, "archived", false, "Only archived repositories (use =false for active)")
	group.AddCommand(command, a.discussionSearchCommand(false), a.discussionSearchCommand(true))
	return group
}
