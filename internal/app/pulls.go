// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strconv"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) prCommand() *cobra.Command {
	group := &cobra.Command{Use: "pr", Short: "Work with pull requests"}
	group.AddCommand(a.pullChecksCommand(), a.pullCheckoutCommand(), a.pullCleanCommand(), a.statusCommand(true))
	var limit, page int
	var state string
	var options services.PullListOptions
	var labels []string
	list := &cobra.Command{Use: "list", Short: "List pull requests", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := oneOf("state", state, "open", "closed", "all"); err != nil {
			return err
		}
		if options.Milestone < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a nonnegative ID"))
		}
		if options.Sort != "" {
			if err := oneOf("sort", options.Sort, "oldest", "recentupdate", "recentclose", "leastupdate", "mostcomment", "leastcomment", "priority"); err != nil {
				return err
			}
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		options.State, options.Limit, options.Page = state, limit, page
		options.Labels, err = (services.Labels{Client: target.client, Repository: target.Repo}).ResolveIssueNames(cmd.Context(), labels)
		if err != nil {
			return err
		}
		items, more, err := (services.Pulls{Client: target.client, Repository: target.Repo}).List(cmd.Context(), options)
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, pullRow); err != nil {
			return err
		}
		return a.morePagesHint(cmd, more, page)
	}}
	list.Flags().StringVar(&state, "state", "open", "Filter state: open, closed, all")
	pageFlags(list, &limit, &page)
	list.Flags().StringVar(&options.Author, "author", "", "Filter by author username")
	list.Flags().StringVar(&options.Base, "base", "", "Filter by target branch")
	list.Flags().StringVar(&options.Sort, "sort", "", "Order: oldest, recentupdate, recentclose, leastupdate, mostcomment, leastcomment, priority")
	list.Flags().Int64Var(&options.Milestone, "milestone", 0, "Filter by milestone ID")
	list.Flags().StringSliceVar(&labels, "label", nil, "Filter by label names (repo: or org: resolves ambiguity)")
	group.AddCommand(list)
	var web, comments bool
	view := &cobra.Command{Use: "view [number]", Short: "View a pull request", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: target.client, Repository: target.Repo}
		var item services.Pull
		if len(args) == 1 {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			item, err = service.View(cmd.Context(), n)
			if err != nil {
				return err
			}
		} else {
			head, err := a.currentBranch(cmd.Context())
			if err != nil {
				return clierr.Wrap(clierr.Usage, errors.New("supply a pull number outside a branch checkout"))
			}
			item, err = service.FindOpenByHead(cmd.Context(), head)
			if err != nil {
				return err
			}
		}
		if web {
			return a.openBrowser(cmd, item.URL)
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		return a.writeDiscussion(cmd, target, pullView(item), comments)
	}}
	view.Flags().BoolVar(&web, "web", false, "Open the pull request in a browser")
	view.Flags().BoolVarP(&comments, "comments", "c", false, "View pull request comments")
	group.AddCommand(view)
	a.addPullMutations(group)
	group.AddCommand(a.pullEditCommand())
	group.AddCommand(a.issueAttachmentCommand(), a.commentCommand())
	return group
}

func pullRow(item services.Pull) []terminal.Cell {
	state := pullState(item)
	return []terminal.Cell{{Text: strconv.FormatInt(item.Number, 10), Link: item.URL}, {Text: item.Title}, {Text: state, Color: stateColors[state]}, {Text: item.Author}}
}

// pullState reports merged pull requests as merged rather than closed.
func pullState(item services.Pull) string {
	if item.Merged {
		return "merged"
	}
	return item.State
}
