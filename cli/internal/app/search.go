// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"

	"github.com/spf13/cobra"
)

func (a *application) searchCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "search",
		Short: "Search within one selected Gitea instance",
		Long: `Search the repositories, issues and pull requests of the selected Gitea instance that your
account can see.`,
	}
	var opts services.RepositorySearchOptions
	var private, archived bool
	command := &cobra.Command{
		Use:   "repo [<query>...]",
		Short: "Search repositories",
		Long:  "Search the names and descriptions of repositories, or their topics with --topic.",
		Example: `# Search repositories about a CLI
$ gt search repo command line

# Search the archived repositories of an organization
$ gt search repo --owner acme --archived

# Search by topic, most recently updated first
$ gt search repo go --topic --sort updated --order desc`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --order applies to an explicit sort only; the default order puts your own repositories first.
			if cmd.Flags().Changed("order") && opts.Sort == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--order requires --sort"))
			}
			if err := a.rejectRepoFlag(cmd, func(owner string) string { return "gt search repo --owner " + owner }); err != nil {
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
			result, err := (services.Repositories{Client: target.client}).Search(cmd.Context(), opts)
			if err != nil {
				return err
			}
			what := "repositories"
			if opts.Query != "" {
				what += " matching " + opts.Query
			}
			spec := listSpec[services.Repository]{Columns: repositoryColumns, Row: repositoryRow, Context: what, Empty: "no " + what}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, opts.Page))
		},
	}
	flags := command.Flags()
	pageFlags(command, &opts.Limit, &opts.Page)
	flags.StringVar(&opts.Owner, "owner", "", "Only repositories of this user or organization")
	enumFlag(command, &opts.Kind, "type", "", "", "Only repositories of this kind", "fork", "source", "mirror")
	enumFlag(command, &opts.Sort, "sort", "", "", "Sort by (default: alphabetical, your own repositories first)", "alpha", "created", "updated", "size", "id")
	enumFlag(command, &opts.Order, "order", "", "asc", "Sort order (requires --sort)", "asc", "desc")
	flags.BoolVar(&opts.Topic, "topic", false, "Match repository topics")
	flags.BoolVar(&private, "private", false, "Only private repositories (use =false for public)")
	flags.BoolVar(&archived, "archived", false, "Only archived repositories (use =false for active)")
	_ = command.RegisterFlagCompletionFunc("owner", a.completeOrgs())
	group.AddCommand(jsonOutput[services.Repository](command), a.discussionSearchCommand(false), a.discussionSearchCommand(true))
	return jsonOutput[services.SearchItem](group)
}
