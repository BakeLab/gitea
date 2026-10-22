// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var errNothingToEdit = clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title (see --help)"))

func (a *application) issueEditCommand() *cobra.Command {
	var title, body, file, deadline, ref, milestone string
	var useEditor, removeMilestone bool
	var metadata metadataFlags
	var addBlockedBy, removeBlockedBy, addProjects, removeProjects []string
	cmd := &cobra.Command{Use: "edit {<number> | <url>}...", Short: "Edit issues: title, body, labels, assignees, milestone, deadline", Long: `Edit one or more issues. Without flags on a terminal, a form edits one issue at a time,
starting from its current values. Label and assignee flags add to or remove from each issue's
current values; the --set flags replace them. The blocked_by and blocks JSON fields cost extra
requests, made only when the output includes them (see gt help formatting).

Gitea changes the milestone, assignees and due date only for people who can write to the issues
of the repository, and leaves them as they were for the author of an issue who cannot: gt then
names the flags or form fields it left out and fails. Gitea refuses their label changes outright,
so gt sends the other changes first.`, Example: `$ gt issue edit 12 --title "Crash on start" --add-label bug
$ gt issue edit 12 13 --add-assignee @me --milestone v1.0
$ gt issue edit 12 --remove-label wontfix --remove-milestone
$ gt issue edit 12 --add-blocked-by 9`, Annotations: map[string]string{"gt.arguments": issueArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
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
		blockers, err := dependencyChanges(addBlockedBy, removeBlockedBy)
		if err != nil {
			return err
		}
		if err := validateProjectChanges(addProjects, removeProjects); err != nil {
			return err
		}
		editing := opts != (services.IssueEdit{}) || useEditor || metadata.changed(cmd) || milestone != ""
		if !editing && len(addBlockedBy)+len(removeBlockedBy)+len(addProjects)+len(removeProjects) == 0 {
			return errNothingToEdit
		}
		target, numbers, err := a.resolveRefs(cmd, args, issueRef)
		if err != nil {
			return err
		}
		if blockers, err = a.resolveDependencies(target, numbers, blockers); err != nil {
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
		boards, err := loadProjectChanges(cmd.Context(), target, addProjects, removeProjects, true)
		if err != nil {
			return err
		}
		names := issueEditFlags(cmd)
		var leftOut []error
		done := batchDone(a, cmd, numbers, issueLabel, func(n int64) (issueDetails, error) {
			for _, blocker := range blockers {
				if err := service.SetBlockedBy(cmd.Context(), n, blocker.IssueReference, blocker.block); err != nil {
					// A missing blocker or repository keeps its own name.
					if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Scope == "" {
						return issueDetails{}, err
					}
					return issueDetails{}, itemError(err, target.Repo)
				}
			}
			var item services.Issue
			if editing {
				item, err = service.Edit(cmd.Context(), n, opts)
			} else {
				item, err = service.View(cmd.Context(), n)
			}
			if err != nil {
				if item.Number != 0 {
					// Gitea refused the labels after taking the other changes.
					leftOut = append(leftOut, editLeftOut(opts, item, target.Repo, names))
				}
				return issueDetails{Issue: item}, itemError(err, target.Repo)
			}
			if editing {
				// The edit stands and is reported; what Gitea left out fails the command after.
				leftOut = append(leftOut, editLeftOut(opts, item, target.Repo, names))
			}
			if _, err := boards.apply(cmd.Context(), item.ID); err != nil {
				return issueDetails{Issue: item}, err
			}
			return a.issueDetails(cmd, service, item)
		}, editedIssue(target))
		return errors.Join(done, errors.Join(leftOut...))
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
	flags.StringVar(&deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339 or Unix timestamp; only the date counts, as the end of that day in UTC; empty clears it")
	flags.StringArrayVar(&addProjects, "add-project", nil, "Add the issue to the project with this `title` (repeatable)")
	flags.StringArrayVar(&removeProjects, "remove-project", nil, "Take the issue off the project with this `title` (repeatable)")
	flags.StringSliceVar(&addBlockedBy, "add-blocked-by", nil, "Mark the issue as blocked by other `issues`: N, #N, OWNER/REPO#N or a URL")
	flags.StringSliceVar(&removeBlockedBy, "remove-blocked-by", nil, "Lift the dependency on other `issues`: N, #N, OWNER/REPO#N or a URL")
	a.completeMetadataFlags(cmd)
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	cmd.ValidArgsFunction = a.completeIssues("open")
	return jsonOutput(cmd, jsonFields[issueDetails]())
}

func editedIssue(t target) func(issueDetails) string {
	return func(item issueDetails) string {
		return "Edited " + discussionNoun(item.Kind) + " " + refLabel(t.Repo, item.Number, item.Title)
	}
}

// issueDetails reads the dependencies of an edited item when the JSON output includes them, as
// issue view does.
func (a *application) issueDetails(cmd *cobra.Command, service services.Issues, item services.Issue) (issueDetails, error) {
	if a.options.json == "" || !a.jsonSelects("blocked_by", "blocks") {
		return issueDetails{Issue: item}, nil
	}
	dependencies, err := service.Dependencies(cmd.Context(), item.Number)
	return issueDetails{Issue: item, BlockedBy: dependencies.BlockedBy, Blocks: dependencies.Blocks}, err
}

func (a *application) editIssuesInteractively(cmd *cobra.Command, target target, numbers []int64) error {
	service := services.Issues{Client: target.client, Repository: target.Repo}
	selectables, err := service.EditSelectables(cmd.Context())
	if err != nil {
		return err
	}
	milestones, labels := milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	edited := editedIssue(target)
	var leftOut []error
	results, failures := applyBatch(cmd, numbers, issueLabel, func(n int64) (details issueDetails, err error) {
		current, err := service.View(cmd.Context(), n)
		if err != nil {
			return details, itemError(err, target.Repo)
		}
		choices := terminal.IssueEditFormChoices{
			Heading: "Editing " + refLabel(target.Repo, n, current.Title),
			Title:   current.Title, Body: current.Body, Assignees: current.Assignees,
			AvailableAssignees: selectables.Assignees, Milestones: milestones, AvailableLabels: labels,
			EditBody: a.bodyEditor(cmd, terminal.BodyTitle),
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
			return details, abortBatch{err}
		}
		opts := changedIssueFields(current, choices, values)
		if opts == (services.IssueEdit{}) {
			a.warn(cmd, "No changes to %s %s", discussionNoun(current.Kind), refLabel(target.Repo, n, current.Title))
			return a.issueDetails(cmd, service, current)
		}
		item, err := service.Edit(cmd.Context(), n, opts)
		if err != nil {
			if item.Number != 0 {
				// Gitea refused the labels after taking the other changes.
				leftOut = append(leftOut, editLeftOut(opts, item, target.Repo, issueMetadataFields))
			}
			return issueDetails{Issue: item}, itemError(err, target.Repo)
		}
		leftOut = append(leftOut, editLeftOut(opts, item, target.Repo, issueMetadataFields))
		if details, err = a.issueDetails(cmd, service, item); err == nil {
			a.done(cmd, "%s", edited(details))
		}
		return details, err
	}, nil)
	return errors.Join(append(append(failures, leftOut...), writeBatchJSON(a, cmd, len(numbers), results))...)
}

// changedIssueFields sets only the fields that the edit form changed, so that an unchanged form
// writes nothing, where writing the current values back would still move the issue's update
// time.
func changedIssueFields(current services.Issue, choices terminal.IssueEditFormChoices, values terminal.IssueEditFormValues) services.IssueEdit {
	var opts services.IssueEdit
	if values.Title != current.Title {
		opts.Title = &values.Title
	}
	if values.Body != current.Body {
		opts.Body = &values.Body
	}
	if !sameElements(values.Assignees, current.Assignees, strings.EqualFold) {
		// Non-nil: an empty selection must clear rather than be omitted.
		assignees := append([]string{}, values.Assignees...)
		opts.Assignees = &assignees
	}
	currentLabels := make([]int64, len(current.Labels))
	for i, label := range current.Labels {
		currentLabels[i] = label.ID
	}
	if !sameElements(values.Labels, currentLabels, func(a, b int64) bool { return a == b }) {
		labels := append([]int64{}, values.Labels...)
		opts.Labels = &labels
	}
	switch {
	case values.ClearMilestone && current.Milestone != nil:
		opts.Milestone = &values.Milestone
	case values.Milestone != 0 && (current.Milestone == nil || current.Milestone.ID != values.Milestone):
		opts.Milestone = &values.Milestone
	}
	switch {
	case values.ClearDeadline && choices.Deadline != nil:
		opts.RemoveDeadline = &values.ClearDeadline
	// The form edits the date, which it shows in the server's offset.
	case values.Deadline != nil && (choices.Deadline == nil || values.Deadline.Format(time.DateOnly) != choices.Deadline.Format(time.DateOnly)):
		due := endOfDay(*values.Deadline)
		opts.Deadline = &due
	}
	return opts
}

// issueEditFlags name the milestone, assignees and due date by the flags of issue edit that asked
// for them.
func issueEditFlags(cmd *cobra.Command) issueMetadataNames {
	names := issueMetadataNames{milestone: "--remove-milestone", deadline: "--deadline"}
	if cmd.Flags().Changed("milestone") {
		names.milestone = "--milestone"
	}
	var assignees []string
	for _, flag := range []string{"set-assignees", "add-assignee", "remove-assignee"} {
		if cmd.Flags().Changed(flag) {
			assignees = append(assignees, "--"+flag)
		}
	}
	names.assignees = strings.Join(assignees, ", ")
	return names
}

// editLeftOut fails when the edited item lacks a change to its milestone, assignees or due date
// that opts asked for, named by names. Gitea leaves these changes out without an error for the
// author of an issue who cannot write to the issues of repository, and the permissions of a
// repository tell only about its code, not its issues. Label changes need no check, as Gitea
// refuses them outright.
func editLeftOut(opts services.IssueEdit, item services.Issue, repository string, names issueMetadataNames) error {
	var missing []string
	var milestone int64
	if item.Milestone != nil {
		milestone = item.Milestone.ID
	}
	if opts.Milestone != nil && *opts.Milestone != milestone {
		missing = append(missing, names.milestone)
	}
	assigned := func(login string) bool {
		return slices.ContainsFunc(item.Assignees, func(assignee string) bool { return strings.EqualFold(assignee, login) })
	}
	var add, remove []string
	if opts.Assignees != nil {
		add = *opts.Assignees
		remove = slices.DeleteFunc(slices.Clone(item.Assignees), func(login string) bool {
			return slices.ContainsFunc(add, func(want string) bool { return strings.EqualFold(want, login) })
		})
	}
	if opts.Changes != nil {
		add = slices.DeleteFunc(slices.Clone(opts.Changes.AddAssignees), func(login string) bool {
			return slices.ContainsFunc(opts.Changes.RemoveAssignees, func(gone string) bool { return strings.EqualFold(gone, login) })
		})
		remove = opts.Changes.RemoveAssignees
	}
	if slices.ContainsFunc(add, func(login string) bool { return !assigned(login) }) || slices.ContainsFunc(remove, assigned) {
		missing = append(missing, names.assignees)
	}
	switch {
	case opts.RemoveDeadline != nil && *opts.RemoveDeadline && item.Deadline != nil:
		missing = append(missing, names.deadline)
	case opts.Deadline != nil && item.Deadline == nil:
		missing = append(missing, names.deadline)
	case opts.Deadline != nil:
		if due, err := time.Parse(time.RFC3339, *item.Deadline); err == nil && !due.Equal(*opts.Deadline) {
			missing = append(missing, names.deadline)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	noun, flags := discussionNoun(item.Kind), strings.Join(missing, ", ")
	them := "them"
	if !strings.Contains(flags, ",") {
		them = "it"
	}
	return fmt.Errorf("%s #%d was edited without %s: Gitea leaves %s out for people who cannot write to the %ss of %s", noun, item.Number, flags, them, noun, repository)
}

// sameElements reports whether a and b hold the same elements, ignoring order.
func sameElements[T any](a, b []T, equal func(T, T) bool) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !slices.ContainsFunc(b, func(y T) bool { return equal(x, y) }) {
			return false
		}
	}
	for _, y := range b {
		if !slices.ContainsFunc(a, func(x T) bool { return equal(x, y) }) {
			return false
		}
	}
	return true
}

// dependencyChange adds or removes the issue that blocks another; an issue without a repository
// is one of the edited issue's repository.
type dependencyChange struct {
	services.IssueReference
	block bool
	// flag and raw are the flag and value that named the issue.
	flag, raw string
}

// dependencyChanges parses --add-blocked-by and --remove-blocked-by, in that order. A repository
// stays as given, such as a URL, until resolveDependencies resolves it.
func dependencyChanges(add, remove []string) ([]dependencyChange, error) {
	var changes []dependencyChange
	for _, list := range []struct {
		flag   string
		values []string
		block  bool
	}{{"add-blocked-by", add, true}, {"remove-blocked-by", remove, false}} {
		for _, raw := range list.values {
			parsed, err := parseRef(raw, issueRef)
			if err != nil || parsed.Number < 1 || (parsed.Repo != "" && !strings.Contains(parsed.Repo, "/")) {
				return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s takes issues as N, #N, OWNER/REPO#N or an issue URL, not %q", list.flag, raw))
			}
			changes = append(changes, dependencyChange{services.IssueReference{Repository: parsed.Repo, Number: parsed.Number}, list.block, list.flag, raw})
		}
	}
	return changes, nil
}

// resolveDependencies resolves the repository of each change to OWNER/REPO on the instance of t,
// whose issues numbers are edited. An issue cannot be both added and removed, nor block itself.
func (a *application) resolveDependencies(t target, numbers []int64, changes []dependencyChange) ([]dependencyChange, error) {
	snapshot, err := a.settings()
	if err != nil {
		return nil, err
	}
	instance, _ := settings.NormalizeHost(t.Host)
	var resolved []dependencyChange
	seen := map[services.IssueReference]bool{}
	for _, change := range changes {
		raw := change.Repository
		change.Repository = t.Repo
		if raw != "" {
			repository, err := settings.ParseRepository(raw, snapshot.Hosts.Hosts)
			if err != nil {
				return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s %s: %w", change.flag, change.raw, err))
			}
			if repository.Host != "" && repository.Host != instance {
				return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s %s is on %s, not on %s with %s", change.flag, change.raw, repository.Host, t.Host, t.Repo))
			}
			change.Repository = repository.Slug()
		}
		if strings.EqualFold(change.Repository, t.Repo) && slices.Contains(numbers, change.Number) {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("--%s %s names the edited issue; an issue cannot block itself", change.flag, change.raw))
		}
		key := services.IssueReference{Repository: strings.ToLower(change.Repository), Number: change.Number}
		if block, ok := seen[key]; ok {
			if block != change.block {
				return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("%s#%d is both added and removed as a blocker", change.Repository, change.Number))
			}
			continue
		}
		seen[key] = change.block
		resolved = append(resolved, change)
	}
	return resolved, nil
}
