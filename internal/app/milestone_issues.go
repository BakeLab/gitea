// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"github.com/spf13/cobra"
)

// milestoneSelector validates a milestone ID or, with byName, a title before any request.
func milestoneSelector(selector string, byName bool) (int64, error) {
	if byName {
		if strings.TrimSpace(selector) == "" {
			return 0, clierr.Wrap(clierr.Usage, errors.New("milestone name cannot be empty"))
		}
		return 0, nil
	}
	return number(selector)
}

// existingMilestone resolves a title, or confirms that an ID exists.
func existingMilestone(ctx context.Context, milestones services.Milestones, selector string, id int64, byName bool) (int64, error) {
	if byName {
		return milestones.ResolveName(ctx, selector)
	}
	_, err := milestones.View(ctx, id)
	return id, err
}

func (a *application) milestoneIssueCommand() *cobra.Command {
	group := &cobra.Command{Use: "issue", Short: "Manage milestone issues and pull requests"}
	opts := services.ListOptions{}
	var byName bool
	list := &cobra.Command{Use: "list <id-or-name>", Short: "List issues and pull requests in a milestone", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := milestoneSelector(args[0], byName)
		if err != nil {
			return err
		}
		if err := oneOf("state", opts.State, "open", "closed", "all"); err != nil {
			return err
		}
		if err := oneOf("kind", opts.Kind, "all", "issues", "pulls"); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		id, err = existingMilestone(cmd.Context(), services.Milestones{Client: target.client, Repository: target.Repo}, args[0], id, byName)
		if err != nil {
			return err
		}
		opts.Milestones = strconv.FormatInt(id, 10)
		items, more, err := (services.Issues{Client: target.client, Repository: target.Repo}).List(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, issueRow); err != nil {
			return err
		}
		return a.morePagesHint(cmd, more, opts.Page)
	}}
	pageFlags(list, &opts.Limit, &opts.Page)
	list.Flags().StringVar(&opts.State, "state", "open", "open, closed, or all")
	list.Flags().StringVar(&opts.Kind, "kind", "all", "all, issues, or pulls")
	list.Flags().BoolVar(&byName, "by-name", false, "Select by exact milestone title instead of ID")
	group.AddCommand(list)
	for _, verb := range []string{"add", "remove"} {
		var byName bool
		command := &cobra.Command{Use: verb + " <id-or-name> <number>...", Short: titleCase(verb) + " issues or pull requests in a milestone", Args: usageArgs(cobra.MinimumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
			id, err := milestoneSelector(args[0], byName)
			if err != nil {
				return err
			}
			numbers, err := resourceNumbers(args[1:])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			id, err = existingMilestone(cmd.Context(), services.Milestones{Client: target.client, Repository: target.Repo}, args[0], id, byName)
			if err != nil {
				return err
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			return batch(a, cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
				if verb == "remove" {
					return service.RemoveMilestone(cmd.Context(), n, id)
				}
				return service.Edit(cmd.Context(), n, services.IssueEdit{Milestone: &id})
			}, issueRow)
		}}
		command.Flags().BoolVar(&byName, "by-name", false, "Select by exact milestone title instead of ID")
		group.AddCommand(command)
	}
	return group
}
