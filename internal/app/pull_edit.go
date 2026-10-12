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

func (a *application) pullEditCommand() *cobra.Command {
	var title, body, file, base string
	var deadline string
	var draft, ready, maintainer bool
	var useEditor bool
	var milestone int64
	var milestoneName string
	var metadata metadataFlags
	var reviewers services.ReviewRequestChanges
	cmd := &cobra.Command{Use: "edit <number>", Short: "Edit pull request content, base, and draft state", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		n, err := number(args[0])
		if err != nil {
			return err
		}
		if draft && ready {
			return clierr.Wrap(clierr.Usage, errors.New("--draft and --ready are mutually exclusive"))
		}
		if err := validateMilestoneFlags(cmd, milestoneName, milestone); err != nil {
			return err
		}
		opts := services.PullEdit{}
		if cmd.Flags().Changed("deadline") {
			opts.Deadline, opts.RemoveDeadline, err = parseDeadline(deadline)
			if err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("add-reviewers") || cmd.Flags().Changed("remove-reviewers") || cmd.Flags().Changed("add-reviewer-teams") || cmd.Flags().Changed("remove-reviewer-teams") {
			trimNames(reviewers.Add.Users, reviewers.Remove.Users, reviewers.Add.Teams, reviewers.Remove.Teams)
			if err := reviewers.Validate(); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			opts.ReviewerChanges = &reviewers
		}
		if cmd.Flags().Changed("milestone") {
			opts.Milestone = &milestone
		}
		if err := metadata.validate(cmd); err != nil {
			return err
		}
		if cmd.Flags().Changed("title") {
			if strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("pull request title cannot be empty"))
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
		if cmd.Flags().Changed("base") {
			if strings.TrimSpace(base) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--base cannot be empty"))
			}
			opts.Base = &base
		}
		if cmd.Flags().Changed("allow-maintainer-edit") {
			opts.AllowMaintainerEdit = &maintainer
		}
		if opts == (services.PullEdit{}) && !useEditor && !draft && !ready && !metadata.changed(cmd) && !cmd.Flags().Changed("milestone-name") {
			return clierr.Wrap(clierr.Usage, errors.New("supply an edit flag; see gt pr edit --help"))
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
		service := services.Pulls{Client: target.client, Repository: target.Repo}
		if draft || ready {
			if opts.Title == nil {
				current, err := service.View(cmd.Context(), n)
				if err != nil {
					return err
				}
				title = current.Title
			}
			if draft {
				title = services.AddDraftPrefix(title)
			} else {
				title = services.StripDraftPrefix(title)
			}
			if strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("draft marker removal leaves an empty title; supply --title"))
			}
			opts.Title = &title
		}
		if useEditor {
			current, err := service.View(cmd.Context(), n)
			if err != nil {
				return err
			}
			editorDraft, err = a.editDraft(cmd, current.Body)
			if err != nil {
				return err
			}
			opts.Body = &editorDraft.Body
		}
		item, err := service.Edit(cmd.Context(), n, opts)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, pullRecord)
	}}
	cmd.Flags().StringVar(&title, "title", "", "Pull request title")
	cmd.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears")
	cmd.Flags().StringVar(&body, "body", "", "Pull request body (empty clears)")
	cmd.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
	cmd.Flags().BoolVar(&useEditor, "editor", false, "Edit the current pull request body in the configured editor")
	cmd.Flags().StringVar(&base, "base", "", "Target branch")
	cmd.Flags().BoolVar(&draft, "draft", false, "Mark the pull request as a draft")
	cmd.Flags().BoolVar(&ready, "ready", false, "Remove the draft title marker")
	cmd.Flags().BoolVar(&maintainer, "allow-maintainer-edit", false, "Allow maintainers to modify the head branch")
	cmd.Flags().StringVar(&milestoneName, "milestone-name", "", "Milestone by exact title; conflicts with --milestone")
	cmd.Flags().Int64Var(&milestone, "milestone", 0, "Milestone ID; 0 clears")
	metadata.register(cmd)
	cmd.Flags().StringSliceVar(&reviewers.Add.Users, "add-reviewers", nil, "Request reviews from users")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Users, "remove-reviewers", nil, "Remove requests for users")
	cmd.Flags().StringSliceVar(&reviewers.Add.Teams, "add-reviewer-teams", nil, "Request reviews from organization teams")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Teams, "remove-reviewer-teams", nil, "Remove requests for organization teams")
	return cmd
}
