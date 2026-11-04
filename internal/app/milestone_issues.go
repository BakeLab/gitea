// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) milestoneIssueCommand() *cobra.Command {
	group := &cobra.Command{Use: "issue", Short: "Manage the issues and pull requests of a milestone", Long: `List the issues and pull requests in a milestone, or add them to it and remove them from
it. Milestones are addressed by title or ID; issues and pull requests by number or URL.`}
	opts := services.ListOptions{Page: 1}
	var byName bool
	list := &cobra.Command{Use: "list <milestone>", Short: "List issues and pull requests in a milestone", Example: `$ gt milestone issue list v1.0
$ gt milestone issue list v1.0 --state all --kind issues`, Annotations: map[string]string{"gt.arguments": milestoneArguments}, Args: nonBlankArgs(cobra.ExactArgs(1), "milestone title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		milestones := services.Milestones{Client: target.client, Repository: target.Repo}
		milestone, err := findMilestone(cmd, milestones, args[0], byName)
		if err != nil {
			return err
		}
		if opts.Milestones, err = milestones.FilterValue(cmd.Context(), milestone); err != nil {
			return err
		}
		page, err := (services.Issues{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), opts)
		if err != nil {
			return err
		}
		where := fmt.Sprintf("in milestone %q of %s", milestone.Title, target.Repo)
		return writeTable(a, cmd, page.Items, issueListSpec(where, opts.State, opts.Kind, false), servicePage(page, opts.Page))
	}}
	pageFlags(list, &opts.Limit, &opts.Page)
	enumFlag(list, &opts.State, "state", "s", "open", "Filter by state", "open", "closed", "all")
	enumFlag(list, &opts.Kind, "kind", "", "all", "List issues, pull requests or both; the kind field of each reads issue or pull", "issues", "pulls", "all")
	list.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a milestone title rather than an ID")
	list.ValidArgsFunction = firstArg(a.completeMilestonesIn("all"))
	group.AddCommand(list)
	for _, verb := range []string{"add", "remove"} {
		var byName bool
		short, preposition, past := "Add issues or pull requests to a milestone", "to", "Added"
		if verb == "remove" {
			short, preposition, past = "Remove issues or pull requests from a milestone", "from", "Removed"
		}
		command := &cobra.Command{Use: verb + " <milestone> {<number> | <url>}...", Short: short, Example: fmt.Sprintf(`$ gt milestone issue %[1]s v1.0 12
$ gt milestone issue %[1]s v1.0 12 13 '#14'`, verb), Annotations: map[string]string{"gt.arguments": milestoneArguments + "\n\n" + issueArguments}, Args: nonBlankArgs(cobra.MinimumNArgs(2), "milestone title or ID", "issue"), RunE: func(cmd *cobra.Command, args []string) error {
			target, numbers, err := a.resolveRefs(cmd, args[1:], issueRef)
			if err != nil {
				return err
			}
			milestone, err := findMilestone(cmd, services.Milestones{Client: target.client, Repository: target.Repo}, args[0], byName)
			if err != nil {
				return err
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			unchanged := map[int64]bool{}
			results, failures := applyBatch(cmd, numbers, issueLabel, func(n int64) (item services.Issue, err error) {
				wanted := milestone.ID
				if verb == "remove" {
					wanted = 0
					item, err = service.RemoveMilestone(cmd.Context(), n, milestone)
					// Like adding an issue that is in the milestone already, this is no failure.
					if notIn, ok := errors.AsType[*services.NotInMilestoneError](err); ok {
						a.warn(cmd, "%s", notIn)
						unchanged[n] = true
						return item, nil
					}
				} else {
					if item, err = service.View(cmd.Context(), n); err == nil && item.Milestone != nil && item.Milestone.ID == milestone.ID {
						a.warn(cmd, "#%d is already in milestone %q", n, milestone.Title)
						unchanged[n] = true
						return item, nil
					}
					if err == nil {
						item, err = service.Edit(cmd.Context(), n, services.IssueEdit{Milestone: &milestone.ID})
					}
				}
				if err != nil {
					return item, itemError(err, target.Repo)
				}
				if editLeftOut(services.IssueEdit{Milestone: &wanted}, item, target.Repo, issueMetadataFields) != nil {
					return item, fmt.Errorf("Gitea left out the milestone: it does so for people who cannot write to the %ss of %s", discussionNoun(item.Kind), target.Repo)
				}
				return item, nil
			}, nil)
			var changed []string
			for _, item := range results {
				if !unchanged[item.Number] {
					changed = append(changed, "#"+strconv.FormatInt(item.Number, 10))
				}
			}
			if len(changed) > 0 {
				a.done(cmd, "%s %s %s %s", past, strings.Join(changed, ", "), preposition, milestoneName(target, milestone))
			}
			return errors.Join(append(failures, writeBatchJSON(a, cmd, len(numbers), results))...)
		}}
		command.Flags().BoolVar(&byName, "by-name", false, "Match a numeric milestone argument as a title rather than an ID")
		milestones, issues := a.completeMilestonesIn("all"), a.completeIssues("open")
		command.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return milestones(cmd, args, toComplete)
			}
			return issues(cmd, args, toComplete)
		}
		group.AddCommand(command)
	}
	return jsonOutput[services.Issue](group)
}
