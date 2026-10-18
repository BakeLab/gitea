// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullEditCommand() *cobra.Command {
	var title, body, file, base, deadline, milestone string
	var draft, ready, maintainer, useEditor, removeMilestone bool
	var metadata metadataFlags
	var reviewers services.ReviewRequestChanges
	cmd := &cobra.Command{Use: "edit [<number> | <url> | <branch>]", Short: "Edit a pull request", Long: `Edit the title, body, base branch, reviewers, labels, assignees, milestone or due date of a
pull request, or mark it as a draft or ready for review.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		if draft && ready {
			return clierr.Wrap(clierr.Usage, errors.New("--draft and --ready are mutually exclusive"))
		}
		if removeMilestone && cmd.Flags().Changed("milestone") {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone and --remove-milestone are mutually exclusive"))
		}
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil && id < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID, or 0 for none"))
		}
		opts := services.PullEdit{}
		var err error
		if cmd.Flags().Changed("deadline") {
			if opts.Deadline, opts.RemoveDeadline, err = parseDeadline(deadline); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("add-reviewer") || cmd.Flags().Changed("remove-reviewer") || cmd.Flags().Changed("add-reviewer-team") || cmd.Flags().Changed("remove-reviewer-team") {
			trimNames(reviewers.Add.Users, reviewers.Remove.Users, reviewers.Add.Teams, reviewers.Remove.Teams)
			if err := reviewers.Validate(); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			opts.ReviewerChanges = &reviewers
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
			if body, err = a.readBody(cmd, body, file); err != nil {
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
		if opts == (services.PullEdit{}) && !useEditor && !draft && !ready && !metadata.changed(cmd) && !cmd.Flags().Changed("milestone") && !removeMilestone {
			return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title (see gt pr edit --help)"))
		}
		t, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		switch {
		case removeMilestone || milestone == "0" || (cmd.Flags().Changed("milestone") && milestone == ""):
			var none int64
			opts.Milestone = &none
		case milestone != "":
			id, err := milestoneID(cmd, t, milestone)
			if err != nil {
				return err
			}
			opts.Milestone = &id
		}
		if opts.ReviewerChanges != nil {
			t.expandMeAll(reviewers.Add.Users)
			t.expandMeAll(reviewers.Remove.Users)
		}
		if opts.Labels, opts.Assignees, opts.Changes, err = metadata.resolve(cmd, t); err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		var current services.Pull
		if ((draft || ready) && opts.Title == nil) || useEditor {
			if current, err = service.View(cmd.Context(), n); err != nil {
				return err
			}
		}
		if draft || ready {
			if opts.Title == nil {
				title = current.Title
			}
			if title = draftTitle(title, draft); strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("removing the draft marker leaves an empty title; pass --title"))
			}
			opts.Title = &title
		}
		if useEditor {
			if editorDraft, err = a.editDraft(cmd, current.Body); err != nil {
				return err
			}
			opts.Body = &editorDraft.Body
		}
		item, err := service.Edit(cmd.Context(), n, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Edited pull request %s", refLabel(t.Repo, n, item.Title))
		return a.jsonResult(cmd, item)
	}}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Pull request title")
	cmd.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears")
	cmd.Flags().StringVarP(&body, "body", "b", "", "Pull request body (empty clears)")
	cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Edit the current body in your editor")
	cmd.Flags().StringVarP(&base, "base", "B", "", "Base branch")
	cmd.Flags().BoolVar(&draft, "draft", false, "Mark the pull request as a draft (same as gt pr ready --undo)")
	cmd.Flags().BoolVar(&ready, "ready", false, "Mark the pull request as ready for review (same as gt pr ready)")
	cmd.Flags().BoolVar(&maintainer, "allow-maintainer-edit", false, "Allow maintainers to push to the head branch")
	cmd.Flags().StringVarP(&milestone, "milestone", "m", "", "Milestone title or ID; 0 removes it")
	cmd.Flags().BoolVar(&removeMilestone, "remove-milestone", false, "Remove the milestone")
	metadata.register(cmd)
	cmd.Flags().StringSliceVar(&reviewers.Add.Users, "add-reviewer", nil, "Request reviews from users; @me for yourself")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Users, "remove-reviewer", nil, "Withdraw review requests from users")
	cmd.Flags().StringSliceVar(&reviewers.Add.Teams, "add-reviewer-team", nil, "Request reviews from teams of the repository's organization")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Teams, "remove-reviewer-team", nil, "Withdraw review requests from teams")
	flagAliases(cmd, map[string]string{"milestone-name": "milestone"})
	for _, name := range []string{"add-reviewer", "remove-reviewer"} {
		_ = cmd.RegisterFlagCompletionFunc(name, a.completeUsers())
	}
	_ = cmd.RegisterFlagCompletionFunc("base", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `$ gt pr edit 13 --title "Add login support" --add-reviewer carol
$ gt pr edit --base develop
$ gt pr edit 13 --editor`
	return cmd
}

// draftTitle marks a title as a draft, the way Gitea recognizes drafts, or removes the marker.
func draftTitle(title string, draft bool) string {
	if draft {
		return services.AddDraftPrefix(title)
	}
	return services.StripDraftPrefix(title)
}

func (a *application) pullReadyCommand() *cobra.Command {
	var undo bool
	cmd := &cobra.Command{Use: "ready [<number> | <url> | <branch>]", Short: "Mark a pull request as ready for review", Long: `Mark a draft pull request as ready for review, or with --undo turn it back into a draft.

Gitea treats a pull request as a draft while its title starts with "WIP:" or "[WIP]"; gt
removes or adds that marker.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		ref := refLabel(t.Repo, n, item.Title)
		if item.Draft == undo {
			if undo {
				a.warn(cmd, "Pull request %s is already a draft", ref)
			} else {
				a.warn(cmd, "Pull request %s is already ready for review", ref)
			}
		} else {
			title := draftTitle(item.Title, undo)
			if strings.TrimSpace(title) == "" {
				return errors.New("removing the draft marker leaves an empty title; set one with: gt pr edit " + strconv.FormatInt(n, 10) + " --title")
			}
			if item, err = service.Edit(cmd.Context(), n, services.PullEdit{Title: &title}); err != nil {
				return err
			}
			if item.Draft != undo {
				return errors.New("the title was changed, but Gitea still " + map[bool]string{true: "does not treat", false: "treats"}[undo] + " the pull request as a draft; this instance may use a different draft prefix, so edit the title with: gt pr edit " + strconv.FormatInt(n, 10) + " --title")
			}
			if undo {
				a.done(cmd, "Converted pull request %s to a draft", refLabel(t.Repo, n, item.Title))
			} else {
				a.done(cmd, "Marked pull request %s as ready for review", refLabel(t.Repo, n, item.Title))
			}
		}
		return a.jsonResult(cmd, item)
	}}
	cmd.Flags().BoolVar(&undo, "undo", false, "Turn the pull request back into a draft")
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `$ gt pr ready
$ gt pr ready 15
$ gt pr ready 15 --undo`
	return cmd
}
