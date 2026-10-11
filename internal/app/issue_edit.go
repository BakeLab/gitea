// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) issueEditCommand() *cobra.Command {
	var title, body, file string
	var useEditor bool
	var deadline string
	var ref string
	var milestone int64
	var milestoneName string
	var metadata metadataFlags
	cmd := &cobra.Command{Use: "edit <number>...", Short: "Edit issue content, assignees, and milestone", Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		numbers, err := resourceNumbers(args)
		if err != nil {
			return err
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		if !localFlagsChanged(cmd) {
			if a.policy(snapshot).CanPrompt(a.deps.Streams) && a.deps.IssueEditForm != nil {
				return a.editIssuesInteractively(cmd, numbers)
			}
			return clierr.Wrap(clierr.Usage, errors.New("supply an edit flag; see tea issue edit --help"))
		}
		if useEditor && len(numbers) != 1 {
			return clierr.Wrap(clierr.Usage, errors.New("--editor requires one issue"))
		}
		if err := validateMilestoneFlags(cmd, milestoneName, milestone); err != nil {
			return err
		}
		opts := services.IssueEdit{}
		if cmd.Flags().Changed("ref") {
			opts.Ref = &ref
		}
		if cmd.Flags().Changed("deadline") {
			opts.Deadline, opts.RemoveDeadline, err = parseDeadline(deadline)
			if err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("title") {
			if strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("issue title cannot be empty"))
			}
			opts.Title = &title
		}
		if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
			body, err = a.readBody(cmd, body, file)
			if err != nil {
				return err
			}
			opts.Body = &body
		}
		if cmd.Flags().Changed("milestone") {
			opts.Milestone = &milestone
		}
		if err := metadata.validate(cmd); err != nil {
			return err
		}
		if opts == (services.IssueEdit{}) && !useEditor && !metadata.changed(cmd) && !cmd.Flags().Changed("milestone-name") {
			return clierr.Wrap(clierr.Usage, errors.New("supply an edit flag; see tea issue edit --help"))
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("milestone-name") {
			milestone, err = (services.Milestones{Client: target.client, Repository: target.Repo}).ResolveName(cmd.Context(), milestoneName)
			if err != nil {
				return err
			}
			opts.Milestone = &milestone
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
			draft, err = a.editDraft(cmd, current.Body)
			if err != nil {
				return err
			}
			opts.Body = &draft.Body
		}
		return batch(a, cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
			return service.Edit(cmd.Context(), n, opts)
		}, issueRow)
	}}
	cmd.Flags().StringVar(&title, "title", "", "Issue title")
	cmd.Flags().StringVar(&ref, "ref", "", "Referenced branch or tag; empty clears")
	cmd.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears")
	cmd.Flags().StringVar(&body, "body", "", "Issue body (empty clears)")
	cmd.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
	cmd.Flags().BoolVar(&useEditor, "editor", false, "Edit the current issue body in the configured editor")
	cmd.Flags().StringVar(&milestoneName, "milestone-name", "", "Milestone by exact title; conflicts with --milestone")
	cmd.Flags().Int64Var(&milestone, "milestone", 0, "Milestone ID; 0 clears")
	metadata.register(cmd)
	return cmd
}

func (a *application) editIssuesInteractively(cmd *cobra.Command, numbers []int64) error {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	service := services.Issues{Client: target.client, Repository: target.Repo}
	selectables, err := service.CreateSelectables(cmd.Context())
	if err != nil {
		return err
	}
	milestones, labels := milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	return batch(a, cmd, numbers, issueLabel, func(n int64) (item services.Issue, err error) {
		current, err := service.View(cmd.Context(), n)
		if err != nil {
			return item, err
		}
		choices := terminal.IssueEditFormChoices{
			Title: current.Title, Body: current.Body, Assignees: current.Assignees,
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
		if err := a.confirm(cmd, fmt.Sprintf("Edit issue %s#%d: %s", target.Repo, n, values.Title)); err != nil {
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
		return service.Edit(cmd.Context(), n, opts)
	}, issueRow)
}
