// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var errNothingToEdit = clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title (see --help)"))

func (a *application) issueEditCommand() *cobra.Command {
	var title, body, file, deadline, ref, milestone string
	var useEditor, removeMilestone bool
	var metadata metadataFlags
	cmd := &cobra.Command{Use: "edit {<number> | <url>}...", Short: "Edit issues: title, body, labels, assignees, milestone, deadline", Long: `Edit one or more issues. Without flags on a terminal, a form edits one issue at a time,
starting from its current values. Label and assignee flags add to or remove from each issue's
current values; the --set flags replace them.`, Example: `$ gt issue edit 12 --title "Crash on start" --add-label bug
$ gt issue edit 12 13 --add-assignee @me --milestone v1.0
$ gt issue edit 12 --remove-label wontfix --remove-milestone`, Annotations: map[string]string{"gt.arguments": issueArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		if !localFlagsChanged(cmd) {
			if !a.canPrompt() || a.deps.IssueEditForm == nil {
				return errNothingToEdit
			}
			target, numbers, err := a.resolveRefs(cmd, args, issueRef)
			if err != nil {
				return err
			}
			return a.editIssuesInteractively(cmd, target, numbers)
		}
		if useEditor && len(args) != 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--editor requires one issue"))
		}
		milestone, removeMilestone, err := milestoneFlag(cmd, milestone)
		if err != nil {
			return err
		}
		opts := services.IssueEdit{}
		if removeMilestone {
			opts.Milestone = new(int64)
		}
		if cmd.Flags().Changed("ref") {
			opts.Ref = &ref
		}
		if opts.Deadline, opts.RemoveDeadline, err = editDeadline(cmd, deadline); err != nil {
			return err
		}
		if cmd.Flags().Changed("title") {
			if strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("issue title cannot be empty"))
			}
			opts.Title = &title
		}
		if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
			if body, err = a.readBody(cmd, body, file); err != nil {
				return err
			}
			opts.Body = &body
		}
		if err := metadata.validate(cmd); err != nil {
			return err
		}
		if opts == (services.IssueEdit{}) && !useEditor && !metadata.changed(cmd) && milestone == "" {
			return errNothingToEdit
		}
		target, numbers, err := a.resolveRefs(cmd, args, issueRef)
		if err != nil {
			return err
		}
		if milestone != "" {
			id, err := milestoneID(cmd, target, milestone)
			if err != nil {
				return err
			}
			opts.Milestone = &id
		}
		if opts.Labels, opts.Assignees, opts.Changes, err = metadata.resolve(cmd, target); err != nil {
			return err
		}
		service := services.Issues{Client: target.client, Repository: target.Repo}
		if useEditor {
			current, err := service.View(cmd.Context(), numbers[0])
			if err != nil {
				return err
			}
			if draft, err = a.editDraft(cmd, current.Body); err != nil {
				return err
			}
			opts.Body = &draft.Body
		}
		return batchDone(a, cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
			item, err := service.Edit(cmd.Context(), n, opts)
			return item, itemError(err, target.Repo)
		}, editedIssue(target))
	}}
	flags := cmd.Flags()
	flags.StringVarP(&title, "title", "t", "", "New title")
	flags.StringVarP(&body, "body", "b", "", "New body; empty clears it")
	flags.StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	flags.BoolVarP(&useEditor, "editor", "e", false, "Edit the current body in the configured editor")
	metadata.register(cmd)
	flags.StringVarP(&milestone, "milestone", "m", "", "Move the issue to a milestone by `title` or ID")
	flags.BoolVar(&removeMilestone, "remove-milestone", false, "Remove the issue from its milestone")
	flags.StringVar(&ref, "ref", "", "Branch or tag the issue refers to; empty clears it")
	flags.StringVar(&deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears it")
	a.completeMetadataFlags(cmd)
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	cmd.ValidArgsFunction = a.completeIssues("open")
	return cmd
}

func editedIssue(t target) func(services.Issue) string {
	return func(item services.Issue) string {
		return "Edited " + discussionNoun(item.Kind) + " " + refLabel(t.Repo, item.Number, item.Title)
	}
}

func (a *application) editIssuesInteractively(cmd *cobra.Command, target target, numbers []int64) error {
	service := services.Issues{Client: target.client, Repository: target.Repo}
	selectables, err := service.CreateSelectables(cmd.Context())
	if err != nil {
		return err
	}
	milestones, labels := milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	return batchDone(a, cmd, numbers, issueLabel, func(n int64) (item services.Issue, err error) {
		current, err := service.View(cmd.Context(), n)
		if err != nil {
			return item, itemError(err, target.Repo)
		}
		choices := terminal.IssueEditFormChoices{
			Heading: "Editing " + refLabel(target.Repo, n, current.Title),
			Title:   current.Title, Body: current.Body, Assignees: current.Assignees,
			CanPush: selectables.CanPush, AvailableAssignees: selectables.Assignees, Milestones: milestones, AvailableLabels: labels,
			EditBody: a.bodyEditor(cmd),
		}
		if current.Milestone != nil {
			choices.Milestone = &terminal.IssueMilestoneChoice{Title: current.Milestone.Title, ID: current.Milestone.ID}
		}
		choices.Labels = labelChoices(current.Labels)
		if current.Deadline != nil {
			if parsed, err := time.Parse(time.RFC3339, *current.Deadline); err == nil {
				choices.Deadline = &parsed
			}
		}
		values, err := a.deps.IssueEditForm.EditIssue(cmd.Context(), choices)
		defer finishDraft(&values.Draft, &err)
		if err != nil {
			return item, abortBatch{err}
		}
		opts := services.IssueEdit{Title: &values.Title, Body: &values.Body}
		if selectables.CanPush {
			// Non-nil slices: an empty selection must clear rather than be omitted.
			assignees, labels := append([]string{}, values.Assignees...), append([]int64{}, values.Labels...)
			opts.Assignees, opts.Labels = &assignees, &labels
			if values.ClearMilestone || values.Milestone != 0 {
				opts.Milestone = &values.Milestone
			}
			opts.Deadline = values.Deadline
			if values.ClearDeadline {
				opts.RemoveDeadline = &values.ClearDeadline
			}
		}
		item, err = service.Edit(cmd.Context(), n, opts)
		return item, itemError(err, target.Repo)
	}, editedIssue(target))
}
