// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) createIssueInteractively(cmd *cobra.Command) (runErr error) {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	service := services.Issues{Client: target.client, Repository: target.Repo}
	selectables, err := service.CreateSelectables(cmd.Context())
	if err != nil {
		return err
	}
	choices := terminal.IssueFormChoices{
		CanPush: selectables.CanPush, Assignees: selectables.Assignees,
		EditBody: a.bodyEditor(cmd),
	}
	for _, template := range selectables.Templates {
		choices.Templates = append(choices.Templates, terminal.IssueTemplateChoice{Name: template.Name, About: template.About, Title: template.Title, Body: issueTemplateBody(template)})
	}
	choices.Milestones, choices.Labels = milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	values, err := a.deps.IssueForm.CreateIssue(cmd.Context(), choices)
	defer finishDraft(&values.Draft, &runErr)
	if err != nil {
		return err
	}
	if err := a.confirm(cmd, "Create issue on "+target.Repo+": "+values.Title); err != nil {
		return err
	}
	item, err := service.Create(cmd.Context(), services.IssueOptions{
		Title: values.Title, Body: values.Body, Assignees: values.Assignees, Milestone: values.Milestone, Labels: values.Labels, Deadline: values.Deadline,
	})
	if err != nil {
		return err
	}
	return writeRecord(a, cmd, item, issueRecord)
}

// issueTemplateBody seeds a body from markdown content, or from the description of form templates.
func issueTemplateBody(template services.IssueTemplate) string {
	if template.Form && template.Content == "" {
		return template.About
	}
	return template.Content
}

// issueTemplate selects a repository issue template by case-insensitive name.
func issueTemplate(ctx context.Context, service services.Issues, name string) (services.IssueTemplate, error) {
	templates, err := service.IssueTemplates(ctx)
	if err != nil {
		return services.IssueTemplate{}, err
	}
	names := make([]string, len(templates))
	for i, template := range templates {
		if strings.EqualFold(template.Name, name) {
			return template, nil
		}
		names[i] = template.Name
	}
	if len(names) == 0 {
		return services.IssueTemplate{}, fmt.Errorf("issue template %q does not exist; %s has no issue templates", name, service.Repository)
	}
	return services.IssueTemplate{}, fmt.Errorf("issue template %q does not exist; available: %s", name, strings.Join(names, ", "))
}

// validateMilestoneFlags checks --milestone and --milestone-name before any request.
func validateMilestoneFlags(cmd *cobra.Command, name string, id int64) error {
	if cmd.Flags().Changed("milestone-name") {
		if cmd.Flags().Changed("milestone") {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone and --milestone-name are mutually exclusive"))
		}
		if strings.TrimSpace(name) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone-name cannot be empty; use --milestone=0 for none"))
		}
	}
	if id < 0 {
		return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a positive ID, or 0 for none"))
	}
	return nil
}

func milestoneChoices(items []services.Milestone) []terminal.IssueMilestoneChoice {
	choices := make([]terminal.IssueMilestoneChoice, len(items))
	for i, item := range items {
		choices[i] = terminal.IssueMilestoneChoice{Title: item.Title, ID: item.ID}
	}
	return choices
}

func labelChoices(items []services.Label) []terminal.IssueLabelChoice {
	choices := make([]terminal.IssueLabelChoice, len(items))
	for i, item := range items {
		choices[i] = terminal.IssueLabelChoice{Name: item.Name, ID: item.ID}
	}
	return choices
}

func issueLabel(n int64) string { return fmt.Sprintf("issue %d", n) }

// metadataFlags are the label and assignee flags shared by issue and pull request edits.
type metadataFlags struct {
	setLabels, addLabels, removeLabels          []string
	setAssignees, addAssignees, removeAssignees []string
}

func (f *metadataFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&f.setLabels, "set-labels", nil, "Replace labels by exact name; repo:/org: disambiguate; empty clears")
	cmd.Flags().StringSliceVar(&f.addLabels, "add-labels", nil, "Add labels by exact name; repo:/org: disambiguate")
	cmd.Flags().StringSliceVar(&f.removeLabels, "remove-labels", nil, "Remove labels by exact name; repo:/org: disambiguate")
	cmd.Flags().StringSliceVar(&f.setAssignees, "set-assignees", nil, "Replace assignees; an empty value clears")
	cmd.Flags().StringSliceVar(&f.addAssignees, "add-assignees", nil, "Add assignees")
	cmd.Flags().StringSliceVar(&f.removeAssignees, "remove-assignees", nil, "Remove assignees")
}

func (f *metadataFlags) validate(cmd *cobra.Command) error {
	for _, kind := range []string{"assignees", "labels"} {
		if cmd.Flags().Changed("set-"+kind) && (cmd.Flags().Changed("add-"+kind) || cmd.Flags().Changed("remove-"+kind)) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--set-%[1]s cannot be combined with --add-%[1]s or --remove-%[1]s", kind))
		}
	}
	if err := requireNames("assignee", "; use --set-assignees= to clear", f.setAssignees, f.addAssignees, f.removeAssignees); err != nil {
		return err
	}
	return requireNames("label", "; use --set-labels= to clear", f.setLabels, f.addLabels, f.removeLabels)
}

// changed reports whether the flags edit anything; empty --add-*/--remove-* values do not.
func (f *metadataFlags) changed(cmd *cobra.Command) bool {
	return cmd.Flags().Changed("set-labels") || cmd.Flags().Changed("set-assignees") || f.incremental()
}

// incremental reports whether any --add-* or --remove-* flag names a value.
func (f *metadataFlags) incremental() bool {
	return len(f.addLabels)+len(f.removeLabels)+len(f.addAssignees)+len(f.removeAssignees) > 0
}

// resolve maps set flags to replacements, and add and remove flags to changes that the service
// merges with each item's current state.
func (f *metadataFlags) resolve(cmd *cobra.Command, t target) (labels *[]int64, assignees *[]string, changes *services.MetadataChanges, err error) {
	resolver := services.Labels{Client: t.client, Repository: t.Repo}
	if cmd.Flags().Changed("set-labels") {
		ids, err := resolver.ResolveIssueNames(cmd.Context(), f.setLabels)
		if err != nil {
			return nil, nil, nil, err
		}
		labels = &ids
	}
	if cmd.Flags().Changed("set-assignees") {
		names := append([]string{}, f.setAssignees...)
		assignees = &names
	}
	if !f.incremental() {
		return labels, assignees, nil, nil
	}
	changes = &services.MetadataChanges{AddAssignees: f.addAssignees, RemoveAssignees: f.removeAssignees}
	if changes.AddLabels, err = resolver.ResolveIssueNames(cmd.Context(), f.addLabels); err != nil {
		return nil, nil, nil, err
	}
	if changes.RemoveLabels, err = resolver.ResolveIssueNames(cmd.Context(), f.removeLabels); err != nil {
		return nil, nil, nil, err
	}
	return labels, assignees, changes, nil
}

func issueRecord(item services.Issue) terminal.Record {
	return numberedRecord(item.Number, item.Title, item.URL)
}

func issueRow(item services.Issue) []terminal.Cell {
	return []terminal.Cell{{Text: strconv.FormatInt(item.Number, 10), Link: item.URL}, {Text: item.Title}, {Text: item.State, Color: stateColors[item.State]}, {Text: item.Author}}
}

func (a *application) issueCommand() *cobra.Command {
	group := &cobra.Command{Use: "issue", Short: "Work with repository issues"}
	opts := services.ListOptions{State: "open", Limit: 30, Page: 1}
	var updated timeRange
	list := &cobra.Command{Use: "list", Short: "List repository issues", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := oneOf("state", opts.State, "open", "closed", "all"); err != nil {
			return err
		}
		if err := oneOf("kind", opts.Kind, "issues", "pulls", "all"); err != nil {
			return err
		}
		var err error
		if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
			return err
		}
		labelNames, err := labelFilter(opts.Labels)
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			if errors.Is(err, errRepositoryRequired) {
				return clierr.Wrap(clierr.Usage, errors.New("repository required: use --repo owner/name, or tea search issue for instance-wide results"))
			}
			return err
		}
		if len(labelNames) > 0 {
			if err := (services.Labels{Client: target.client, Repository: target.Repo}).RequireFilterNames(cmd.Context(), labelNames); err != nil {
				return err
			}
			opts.Labels = strings.Join(labelNames, ",")
		}
		items, more, err := (services.Issues{Client: target.client, Repository: target.Repo}).List(cmd.Context(), opts)
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, issueRow); err != nil {
			return err
		}
		return a.morePagesHint(cmd, more, opts.Page)
	}}
	list.Flags().StringVar(&opts.State, "state", "open", "Issue state: open, closed, all")
	list.Flags().StringVar(&opts.Kind, "kind", "issues", "Resource kind: issues, pulls, or all")
	pageFlags(list, &opts.Limit, &opts.Page)
	list.Flags().StringVar(&opts.Search, "search", "", "Search issue titles and bodies")
	list.Flags().StringVar(&opts.Assignee, "assignee", "", "Filter by assigned username")
	list.Flags().StringVar(&opts.Labels, "label", "", "Filter by comma-separated label names")
	list.Flags().StringVar(&opts.Milestones, "milestone", "", "Filter by comma-separated milestone names or IDs")
	list.Flags().StringVar(&opts.Author, "author", "", "Filter by author username")
	list.Flags().StringVar(&opts.Mentions, "mentions", "", "Filter by mentioned username")
	updated.register(list, "since", "Updated since a date, timestamp, or duration ago", "until", "Updated before a date, timestamp, or duration ago")
	group.AddCommand(list)
	var web, comments bool
	view := &cobra.Command{Use: "view <number>", Short: "View a repository issue", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Issues{Client: target.client, Repository: target.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		if web {
			return a.openBrowser(cmd, item.URL)
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		dependencies, err := service.Dependencies(cmd.Context(), n)
		if err != nil {
			return err
		}
		return a.writeDiscussion(cmd, target, issueView(item, target.Repo, dependencies), comments)
	}}
	view.Flags().BoolVar(&web, "web", false, "Open the issue in a browser")
	view.Flags().BoolVarP(&comments, "comments", "c", false, "View issue comments")
	group.AddCommand(view)
	var title, body, bodyFile, deadline, templateName string
	var useEditor bool
	var ref string
	var assignees, labelNames, attachmentPaths []string
	var milestone int64
	var milestoneName string
	create := &cobra.Command{Use: "create", Short: "Create a repository issue", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		if err := validateMilestoneFlags(cmd, milestoneName, milestone); err != nil {
			return err
		}
		if err := requireNames("label", "", labelNames); err != nil {
			return err
		}
		if err := requireNames("assignee", "", assignees); err != nil {
			return err
		}
		if cmd.Flags().Changed("template") && strings.TrimSpace(templateName) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--template cannot be empty"))
		}
		files, err := openRegularFiles(attachmentPaths)
		defer closeFiles(files)
		if err != nil {
			return err
		}
		var due *time.Time
		if cmd.Flags().Changed("deadline") {
			due, _, err = parseDeadline(deadline)
			if err != nil {
				return err
			}
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		policy := a.policy(snapshot)
		if !localFlagsChanged(cmd) && policy.CanPrompt(a.deps.Streams) && a.deps.IssueForm != nil {
			return a.createIssueInteractively(cmd)
		}
		var resolved target
		var templateBody string
		if cmd.Flags().Changed("template") {
			if resolved, err = a.resolve(cmd.Context(), true); err != nil {
				return err
			}
			template, err := issueTemplate(cmd.Context(), services.Issues{Client: resolved.client, Repository: resolved.Repo}, strings.TrimSpace(templateName))
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("title") {
				title = template.Title
			}
			templateBody = issueTemplateBody(template)
		}
		if bodyFile == "-" && title == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--body-file - requires --title; stdin cannot also supply a prompt"))
		}
		prompted, err := a.require(cmd, &title, "title")
		if err != nil {
			return err
		}
		switch {
		case useEditor:
			draft, err = a.editDraft(cmd, templateBody)
			body = draft.Body
		case cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file"):
			body, err = a.readBody(cmd, body, bodyFile)
		default:
			body = templateBody
		}
		if err != nil {
			return err
		}
		target := resolved
		if target.client == nil {
			if target, err = a.resolve(cmd.Context(), true); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("milestone-name") {
			milestone, err = (services.Milestones{Client: target.client, Repository: target.Repo}).ResolveName(cmd.Context(), milestoneName)
			if err != nil {
				return err
			}
		}
		labelIDs, err := (services.Labels{Client: target.client, Repository: target.Repo}).ResolveIssueNames(cmd.Context(), labelNames)
		if err != nil {
			return err
		}
		if prompted {
			if err := a.confirm(cmd, "Create issue on "+target.Repo+": "+title); err != nil {
				return err
			}
		}
		service := services.Issues{Client: target.client, Repository: target.Repo}
		item, err := service.Create(cmd.Context(), services.IssueOptions{Title: title, Body: body, Deadline: due, Ref: ref, Assignees: assignees, Milestone: milestone, Labels: labelIDs})
		if err != nil {
			return err
		}
		var failures []error
		for _, file := range files {
			if _, err := service.UploadAttachment(cmd.Context(), item.Number, filepath.Base(file.Name()), file); err != nil {
				failures = append(failures, fmt.Errorf("issue %d created; attachment %s upload failed: %w", item.Number, filepath.Base(file.Name()), err))
			}
		}
		if err := writeRecord(a, cmd, item, issueRecord); err != nil {
			return err
		}
		return errors.Join(failures...)
	}}
	create.Flags().StringVar(&title, "title", "", "Issue title")
	create.Flags().StringVar(&ref, "ref", "", "Referenced branch or tag")
	create.Flags().StringSliceVar(&labelNames, "label", nil, "Apply labels by exact name; use repo: or org: to disambiguate")
	create.Flags().StringSliceVar(&assignees, "assignee", nil, "Assign users by username")
	create.Flags().StringVar(&milestoneName, "milestone-name", "", "Milestone by exact title; conflicts with --milestone")
	create.Flags().Int64Var(&milestone, "milestone", 0, "Milestone ID")
	create.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp")
	create.Flags().StringVar(&body, "body", "", "Issue body")
	create.Flags().StringVar(&bodyFile, "body-file", "", "Read body from a file, or - for stdin")
	create.Flags().BoolVar(&useEditor, "editor", false, "Compose the issue body in the configured editor")
	create.Flags().StringVar(&templateName, "template", "", "Seed title and body from the repository issue template with this name")
	create.Flags().StringArrayVar(&attachmentPaths, "attachment", nil, "File to attach after creation (repeatable)")
	group.AddCommand(create)
	for _, action := range []string{"close", "reopen"} {
		group.AddCommand(&cobra.Command{Use: action + " <number>...", Short: titleCase(action) + " repository issues", Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			numbers, err := resourceNumbers(args)
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			state := "closed"
			if action == "reopen" {
				state = "open"
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			return batch(a, cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
				return service.SetState(cmd.Context(), n, state)
			}, issueRow)
		}})
	}
	group.AddCommand(a.issueAttachmentCommand(), a.commentCommand(), a.issueEditCommand())
	return group
}
