// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) discussionSearchCommand(pulls bool) *cobra.Command {
	noun, nouns, alias := "issue", "issues", "issues"
	if pulls {
		noun, nouns, alias = "pr", "pull requests", "prs"
	}
	var opts services.DiscussionSearchOptions
	var labels []string
	var updated timeRange
	opts.Pulls = pulls
	example := fmt.Sprintf(`$ gt search %[1]s "login error"
# Search one repository; its labels and milestones are checked
$ gt search %[1]s crash -R alice/project --label bug --milestone v1.0`, noun)
	if pulls {
		example += "\n$ gt search pr --review-requested"
	}
	command := &cobra.Command{Use: noun + " [<query>...]", Aliases: []string{alias}, Short: "Search " + nouns, Long: fmt.Sprintf(`Search the %[1]s of every repository you can access on the instance, or of one
repository with --repo. Words of the query match titles and bodies.`, nouns), Example: example, RunE: func(cmd *cobra.Command, args []string) error {
		if opts.Author != "" && opts.Authored {
			return clierr.Wrap(clierr.Usage, errors.New("--author cannot be combined with --authored"))
		}
		var err error
		if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
			return err
		}
		if err := requireNames("label", "", labels); err != nil {
			return err
		}
		milestones, err := nameFilter("milestone", opts.Milestones)
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		if target.Repo != "" && opts.Owner != "" && !strings.EqualFold(target.owner(), opts.Owner) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--owner %s cannot be combined with repository %s", opts.Owner, target.Repo))
		}
		if opts.Team != "" && opts.Owner == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--team requires --owner"))
		}
		if (opts.Team != "" || opts.ReviewRequested || opts.Reviewed) && target.Repo != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--team, --review-requested and --reviewed require instance-wide search; omit --repo or GITEA_CLI_REPO"))
		}
		opts.Author = target.expandMe(opts.Author)
		if target.Repo != "" {
			if opts.Labels, opts.Milestones, err = issueFilters(cmd, target, labels, milestones); err != nil {
				return err
			}
		} else if len(labels)+len(milestones) > 0 {
			opts.Labels, opts.Milestones = strings.Join(labels, ","), strings.Join(milestones, ",")
			a.warn(cmd, "Instance-wide search ignores --label and --milestone values that match nothing; pass --repo to check them")
		}
		opts.Query = strings.Join(args, " ")
		page, err := (services.Issues{Client: target.client, Repository: target.Repo}).SearchPage(cmd.Context(), opts)
		if err != nil {
			return err
		}
		where := "on " + strings.TrimPrefix(strings.TrimPrefix(target.Host, "https://"), "http://")
		if target.Repo != "" {
			where = "in " + target.Repo
		}
		if opts.Query != "" {
			where = fmt.Sprintf("matching %q %s", opts.Query, where)
		}
		what := nouns
		if opts.State != "all" {
			what = opts.State + " " + nouns
		}
		return writeTable(a, cmd, page.Items, listSpec[services.SearchItem]{
			Columns: []terminal.Column{{Header: "Repo#ID"}, {Header: "Title", Flex: true}, {Header: "State"}, {Header: "Author"}, {Header: "Updated"}},
			Row:     searchRow, Empty: "no " + what + " " + where, Context: what + " " + where,
		}, servicePage(page, opts.Page))
	}}
	flags := command.Flags()
	pageFlags(command, &opts.Limit, &opts.Page)
	enumFlag(command, &opts.State, "state", "s", "open", "Filter by state", "open", "closed", "all")
	flags.StringVar(&opts.Owner, "owner", "", "Search only the repositories of this `owner`")
	flags.StringVar(&opts.Team, "team", "", "Search only the repositories of this organization `team` (requires --owner)")
	flags.StringVarP(&opts.Author, "author", "A", "", "Filter by author; @me for yourself")
	flags.StringSliceVarP(&labels, "label", "l", nil, "Filter by label `names`")
	flags.StringVarP(&opts.Milestones, "milestone", "m", "", "Filter by milestone `titles`, comma-separated")
	updated.register(command, "since", "Updated since a date, timestamp, or duration ago", "until", "Updated before a date, timestamp, or duration ago")
	flags.BoolVar(&opts.Assigned, "assigned", false, "Assigned to you")
	flags.BoolVar(&opts.Authored, "authored", false, "Created by you")
	flags.BoolVar(&opts.Mentioned, "mentioned", false, "Mentioning you")
	if pulls {
		flags.BoolVar(&opts.ReviewRequested, "review-requested", false, "Requesting your review (instance-wide search)")
		flags.BoolVar(&opts.Reviewed, "reviewed", false, "Reviewed by you (instance-wide search)")
	}
	_ = command.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = command.RegisterFlagCompletionFunc("milestone", commaSeparated(a.completeMilestones()))
	_ = command.RegisterFlagCompletionFunc("author", a.completeUsers())
	_ = command.RegisterFlagCompletionFunc("owner", a.completeOrgs())
	return command
}

func searchRow(item services.SearchItem) []terminal.Cell {
	return []terminal.Cell{
		{Text: fmt.Sprintf("%s#%d", item.Repository, item.Number), Link: item.URL, Color: stateColors[item.State]},
		{Text: item.Title},
		{Text: item.State, Color: stateColors[item.State]},
		{Text: item.Author},
		{Time: terminal.ParseTime(item.UpdatedAt)},
	}
}
