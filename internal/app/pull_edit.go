// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
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
	var addProjects, removeProjects []string
	cmd := &cobra.Command{Use: "edit [<number> | <url> | <branch>]", Short: "Edit a pull request", Long: `Edit the title, body, base branch, reviewers, labels, assignees, milestone or due date of a
pull request, or mark it as a draft or ready for review.

Gitea changes the milestone, assignees and due date only for people who can write to the pull
requests of the repository, and leaves them as they were for the author of a pull request who
cannot: gt then names the flags it left out and fails. Gitea refuses their label changes
outright, so gt sends the other changes first.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		if draft && ready {
			return clierr.Wrap(clierr.Usage, errors.New("--draft cannot be combined with --ready"))
		}
		if removeMilestone && cmd.Flags().Changed("milestone") {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone cannot be combined with --remove-milestone"))
		}
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil && id < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID, or 0 for none"))
		}
		opts := services.PullEdit{}
		var err error
		if opts.Deadline, opts.RemoveDeadline, err = editDeadline(cmd, deadline); err != nil {
			return err
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
		if err := requireNames("project", "", addProjects, removeProjects); err != nil {
			return err
		}
		if opts == (services.PullEdit{}) && !useEditor && !draft && !ready && !metadata.changed(cmd) && !cmd.Flags().Changed("milestone") && !removeMilestone && len(addProjects)+len(removeProjects) == 0 {
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
		// before is the pull request ahead of the edit, when read: it tells a failed edit what
		// was saved.
		var current services.Pull
		var before *services.Pull
		// Without --draft or --ready, a new title keeps the draft state, which Gitea reads from it.
		keepDraft := opts.Title != nil && !draft && !ready
		if ((draft || ready) && opts.Title == nil) || keepDraft || useEditor {
			if current, err = service.View(cmd.Context(), n); err != nil {
				return err
			}
			before = &current
		}
		if keepDraft {
			draft = current.Draft
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
		boards, err := loadProjectChanges(cmd.Context(), t, addProjects, removeProjects, true)
		if err != nil {
			return err
		}
		var item services.Pull
		var failures []error
		if opts == (services.PullEdit{}) {
			// Only project boards change: there is nothing to send to the pull request itself.
			if _, err := boards.applyToNumber(cmd.Context(), n); err != nil {
				return err
			}
			item, err = service.View(cmd.Context(), n)
		} else if item, err = service.EditFrom(cmd.Context(), n, opts, before); err == nil || item.Number != 0 {
			// Boards change only after the edit, so that a rejected edit leaves them as they were;
			// one that Gitea took in part changes them like the labels.
			// Boards that already held the pull request, or lacked it, are not named as saved.
			if sent, boardsErr := boards.applyToNumber(cmd.Context(), n); boardsErr != nil {
				failures = append(failures, boardsErr)
			} else if partial, ok := errors.AsType[*services.PartialEditError](err); ok && sent {
				partial.Saved = append(partial.Saved, "the project boards")
			}
		}
		if err != nil {
			if item.Number == 0 && len(addProjects)+len(removeProjects) > 0 {
				return fmt.Errorf("%w; the project boards were not changed", err)
			}
			if item.Number != 0 {
				// Gitea refused the labels or the fields of the pull request itself after taking the
				// other changes. The error names what was not changed; what Gitea left out is told
				// apart from what it refused only when every request succeeded.
				return errors.Join(err, followUpError(fmt.Sprintf("pull request #%d", n), "edited", failures))
			}
			return err
		}
		a.done(cmd, "Edited pull request %s", refLabel(t.Repo, n, item.Title))
		// The edit stands and is reported; what Gitea left out fails the command after.
		return errors.Join(followUpError(fmt.Sprintf("pull request #%d", n), "edited", failures), pullEditLeftOut(cmd, n, opts, item, t.Repo), a.jsonResult(cmd, item))
	}}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Pull request title")
	cmd.Flags().StringVar(&deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339 or Unix timestamp; only the date counts, as the end of that day in UTC; empty clears it")
	cmd.Flags().StringVarP(&body, "body", "b", "", "Pull request body (empty clears)")
	cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Edit the current body in your editor")
	cmd.Flags().StringVarP(&base, "base", "B", "", "Base branch")
	cmd.Flags().BoolVar(&draft, "draft", false, "Mark the pull request as a draft (same as gt pr ready --undo)")
	cmd.Flags().BoolVar(&ready, "ready", false, "Mark the pull request as ready for review (same as gt pr ready)")
	cmd.Flags().BoolVar(&maintainer, "allow-maintainer-edit", false, "Allow maintainers to push to the head branch")
	cmd.Flags().StringArrayVar(&addProjects, "add-project", nil, "Add the pull request to the project with this `title` (repeatable)")
	cmd.Flags().StringArrayVar(&removeProjects, "remove-project", nil, "Take the pull request off the project with this `title` (repeatable)")
	cmd.Flags().StringVarP(&milestone, "milestone", "m", "", "Milestone title or ID; 0 removes it")
	cmd.Flags().BoolVar(&removeMilestone, "remove-milestone", false, "Remove the milestone")
	metadata.register(cmd)
	cmd.Flags().StringSliceVar(&reviewers.Add.Users, "add-reviewer", nil, "Request reviews from users; @me for yourself")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Users, "remove-reviewer", nil, "Withdraw review requests from users")
	cmd.Flags().StringSliceVar(&reviewers.Add.Teams, "add-reviewer-team", nil, "Request reviews from teams of the repository's organization")
	cmd.Flags().StringSliceVar(&reviewers.Remove.Teams, "remove-reviewer-team", nil, "Withdraw review requests from teams")
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

// pullEditLeftOut fails, as issue edit does, when pull request n, edited without an error, lacks
// a change to its milestone, assignees or due date that opts asked for. Gitea leaves them out
// without an error for an author who cannot write to the pull requests, and drops the due date
// too when a milestone sends the edit through the issue endpoint.
func pullEditLeftOut(cmd *cobra.Command, n int64, opts services.PullEdit, item services.Pull, repository string) error {
	return editLeftOut(services.IssueEdit{
		Assignees: opts.Assignees, Milestone: opts.Milestone, Deadline: opts.Deadline, RemoveDeadline: opts.RemoveDeadline, Changes: opts.Changes,
	}, services.Issue{
		Kind: services.KindPull, Number: n, Assignees: item.Assignees, Milestone: item.Milestone, Deadline: item.Deadline,
	}, repository, issueEditFlags(cmd))
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
