// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) discussionSearchCommand(pulls bool) *cobra.Command {
	noun := "issue"
	if pulls {
		noun = "pr"
	}
	var opts services.DiscussionSearchOptions
	var updated timeRange
	opts.Pulls = pulls
	command := &cobra.Command{Use: noun + " [query]...", Short: "Search " + noun + "s across the selected instance or repository", RunE: func(cmd *cobra.Command, args []string) error {
		if err := oneOf("state", opts.State, "open", "closed", "all"); err != nil {
			return err
		}
		if opts.Author != "" && opts.Authored {
			return clierr.Wrap(clierr.Usage, errors.New("--author and --authored are mutually exclusive"))
		}
		var err error
		if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
			return err
		}
		labelNames, err := labelFilter(opts.Labels)
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		if target.Repo != "" && opts.Owner != "" && !strings.EqualFold(strings.SplitN(target.Repo, "/", 2)[0], opts.Owner) {
			return clierr.Wrap(clierr.Usage, errors.New("--owner conflicts with selected repository"))
		}
		if opts.Team != "" && opts.Owner == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--team requires --owner"))
		}
		if opts.Team != "" && target.Repo != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--team requires instance-wide search; omit --repo or TEA_REPO"))
		}
		if len(labelNames) > 0 {
			if target.Repo != "" {
				if err := (services.Labels{Client: target.client, Repository: target.Repo}).RequireFilterNames(cmd.Context(), labelNames); err != nil {
					return err
				}
			}
			opts.Labels = strings.Join(labelNames, ",")
		}
		opts.Query = strings.Join(args, " ")
		items, err := (services.Issues{Client: target.client, Repository: target.Repo}).Search(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, searchRow)
	}}
	flags := command.Flags()
	pageFlags(command, &opts.Limit, &opts.Page)
	flags.StringVar(&opts.State, "state", "open", "State: open, closed, all")
	flags.StringVar(&opts.Owner, "owner", "", "Repository owner")
	flags.StringVar(&opts.Team, "team", "", "Organization team name (instance search; requires --owner)")
	flags.StringVar(&opts.Author, "author", "", "Issue/PR author")
	flags.StringVar(&opts.Labels, "label", "", "Comma-separated label names")
	flags.StringVar(&opts.Milestones, "milestone", "", "Comma-separated milestone names")
	updated.register(command, "since", "Updated since a date, timestamp, or duration ago", "before", "Updated before a date, timestamp, or duration ago")
	flags.BoolVar(&opts.Assigned, "assigned", false, "Assigned to the selected account")
	flags.BoolVar(&opts.Authored, "authored", false, "Created by the selected account")
	flags.BoolVar(&opts.Mentioned, "mentioned", false, "Mentioning the selected account")
	if pulls {
		flags.BoolVar(&opts.ReviewRequested, "review-requested", false, "Requesting review from the selected account (instance search)")
		flags.BoolVar(&opts.Reviewed, "reviewed", false, "Reviewed by the selected account (instance search)")
	}
	return command
}

func searchRow(item services.SearchItem) []terminal.Cell {
	return []terminal.Cell{{Text: fmt.Sprintf("%s#%d", item.Repository, item.Number), Link: item.URL}, {Text: item.Title}, {Text: item.State, Color: stateColors[item.State]}}
}
