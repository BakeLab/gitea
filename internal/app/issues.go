// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// issueArguments is the ARGUMENTS help of commands that take issue references.
const issueArguments = `An issue can be supplied as argument in any of the following formats:
- by number, e.g. "123" or "#123"
- by URL, e.g. "https://gitea.com/OWNER/REPO/issues/123"
- by repository and number, e.g. "OWNER/REPO#123"`

func (a *application) issueCommand() *cobra.Command {
	group := &cobra.Command{Use: "issue", Short: "Manage issues", Long: `Work with the issues of a repository: list, view and create them, edit, close or reopen
one or more, and comment on them. Gitea lists pull requests among issues, so most issue
commands also accept a pull request number.`}
	group.AddCommand(a.issueListCommand(), a.issueViewCommand(), a.issueCreateCommand(), a.issueEditCommand(),
		a.issueStateCommand("close"), a.issueStateCommand("reopen"), a.issueAttachmentCommand(issueRef), a.commentCommand(issueRef))
	return group
}

func (a *application) issueListCommand() *cobra.Command {
	opts := services.ListOptions{Page: 1}
	var labels []string
	var updated timeRange
	var web bool
	cmd := &cobra.Command{Use: "list", Short: "List issues in a repository", Long: `List the issues of a repository, newest first. Filters combine: an issue must match all of them.
Labels and milestones must exist in the repository.`, Example: `$ gt issue list --label bug --state all
$ gt issue list --assignee @me --json number,title
# Issues and pull requests in a milestone, as tab-separated values
$ gt issue list --kind all --milestone v1.0 | cut -f1,2
$ gt issue list --search "login" --web`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		var err error
		if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
			return err
		}
		if err := requireNames("label", "", labels); err != nil {
			return err
		}
		milestones, err := nameFilter("milestone", opts.Milestones)
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			if errors.Is(err, errRepositoryRequired) {
				return clierr.Wrap(clierr.Usage, errors.New("repository required: use --repo owner/name, or gt search issue for instance-wide results"))
			}
			return err
		}
		opts.Assignee, opts.Author, opts.Mentions = target.expandMe(opts.Assignee), target.expandMe(opts.Author), target.expandMe(opts.Mentions)
		if web {
			return a.openBrowser(cmd, issuesURL(target, opts))
		}
		filtered := opts.Search+opts.Assignee+opts.Author+opts.Mentions != "" || len(labels)+len(milestones) > 0 || opts.Since != nil || opts.Before != nil
		if opts.Labels, opts.Milestones, err = issueFilters(cmd, target, labels, milestones); err != nil {
			return err
		}
		page, err := (services.Issues{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return writeTable(a, cmd, page.Items, issueListSpec("in "+target.Repo, opts.State, opts.Kind, filtered), servicePage(page, opts.Page))
	}}
	enumFlag(cmd, &opts.State, "state", "s", "open", "Filter by state", "open", "closed", "all")
	enumFlag(cmd, &opts.Kind, "kind", "", "issues", "List issues, pull requests or both", "issues", "pulls", "all")
	pageFlags(cmd, &opts.Limit, &opts.Page)
	flags := cmd.Flags()
	flags.StringVarP(&opts.Search, "search", "S", "", "Search issue titles and bodies")
	flags.StringVarP(&opts.Assignee, "assignee", "a", "", "Filter by assignee; @me for yourself")
	flags.StringVarP(&opts.Author, "author", "A", "", "Filter by author; @me for yourself")
	flags.StringVar(&opts.Mentions, "mentions", "", "Filter by mentioned user; @me for yourself")
	flags.StringSliceVarP(&labels, "label", "l", nil, "Filter by label `names`")
	flags.StringVarP(&opts.Milestones, "milestone", "m", "", "Filter by milestone `titles` or IDs, comma-separated")
	updated.register(cmd, "since", "Updated since a date, timestamp, or duration ago", "until", "Updated before a date, timestamp, or duration ago")
	flags.BoolVarP(&web, "web", "w", false, "Open the issue list in the browser")
	_ = cmd.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = cmd.RegisterFlagCompletionFunc("milestone", commaSeparated(a.completeMilestones()))
	for _, name := range []string{"assignee", "author", "mentions"} {
		_ = cmd.RegisterFlagCompletionFunc(name, a.completeUsers())
	}
	return cmd
}

// issuesURL is the web page of the repository's issues or pull requests in state, searched for
// the --search text.
func issuesURL(t target, opts services.ListOptions) string {
	section := "issues"
	if opts.Kind == "pulls" {
		section = "pulls"
	}
	query := url.Values{"state": {opts.State}}
	if opts.Search != "" {
		query.Set("q", opts.Search)
	}
	return repositoryURL(t) + "/" + section + "?" + query.Encode()
}

// issueListSpec describes issue tables of the issues where, such as "in alice/x": STATE shows on a
// terminal unless only open issues are listed, and KIND when issues and pull requests are; piped
// tables always have both.
func issueListSpec(where, state, kind string, filtered bool) listSpec[services.Issue] {
	what := map[string]string{"issues": "issues", "pulls": "pull requests", "all": "issues and pull requests"}[kind]
	if state != "all" {
		what = state + " " + what
	}
	empty := "no " + what
	if filtered {
		empty += " match your filters"
	}
	return listSpec[services.Issue]{
		Columns: []terminal.Column{
			{Header: "ID", Prefix: "#"},
			{Header: "Title", Flex: true},
			{Header: "Labels", Flex: true},
			{Header: "State", PipedOnly: state == "open"},
			{Header: "Kind", PipedOnly: kind != "all"},
			{Header: "Author"},
			{Header: "Updated"},
		},
		Row:     issueRow,
		Empty:   empty + " " + where,
		Context: what + " " + where,
	}
}

func issueRow(item services.Issue) []terminal.Cell {
	labels := make([]string, len(item.Labels))
	for i, label := range item.Labels {
		labels[i] = label.Name
	}
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.Number, 10), Link: item.URL, Color: stateColors[item.State]},
		{Text: item.Title},
		{Text: strings.Join(labels, ", ")},
		{Text: item.State, Color: stateColors[item.State]},
		{Text: item.Kind},
		{Text: item.Author},
		{Time: terminal.ParseTime(item.UpdatedAt)},
	}
}

func (a *application) issueViewCommand() *cobra.Command {
	var web, comments bool
	cmd := &cobra.Command{Use: "view {<number> | <url>}", Short: "View an issue", Long: `Display the title, body and other information about an issue. With --comments, the
conversation follows the body.`, Example: `$ gt issue view 12
$ gt issue view 12 --comments
$ gt issue view https://gitea.com/OWNER/REPO/issues/12 --web`, Annotations: map[string]string{"gt.arguments": issueArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		target, n, err := a.resolveRef(cmd, args[0], issueRef)
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
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the issue in the browser")
	cmd.Flags().BoolVarP(&comments, "comments", "c", false, "View issue comments")
	cmd.ValidArgsFunction = firstArg(a.completeIssues("open"))
	return cmd
}

func (a *application) issueCreateCommand() *cobra.Command {
	var title, body, bodyFile, deadline, templateName, ref, milestone string
	var useEditor, web bool
	var assignees, labelNames, attachmentPaths []string
	cmd := &cobra.Command{Use: "create", Aliases: []string{"new"}, Short: "Create an issue", Long: `Create an issue in a repository. Without flags on a terminal, a form asks for the title and
body, then optionally for assignees, a milestone, labels and a due date. The new issue's URL
is printed, so scripts can capture it.`, Example: `$ gt issue create
$ gt issue create --title "Crash on start" --body "Steps to reproduce…" --label bug --milestone v1.0
$ gt issue create -t "Update docs" -F notes.md -a @me
# Start from a repository issue template, or continue in the browser
$ gt issue create --template "Bug report"
$ gt issue create --title "Idea" --web`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		milestone, _, err := milestoneFlag(cmd, milestone)
		if err != nil {
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
			if due, _, err = parseDeadline(deadline); err != nil {
				return err
			}
		}
		prompt := a.canPrompt()
		if !localFlagsChanged(cmd) && prompt && a.deps.IssueForm != nil {
			return a.createIssueInteractively(cmd)
		}
		var target target
		var templateBody string
		if cmd.Flags().Changed("template") || web {
			if target, err = a.resolve(cmd.Context(), true); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("template") {
			template, err := issueTemplate(cmd.Context(), services.Issues{Client: target.client, Repository: target.Repo}, strings.TrimSpace(templateName))
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("title") {
				title = template.Title
			}
			templateBody = issueTemplateBody(template)
		}
		if web {
			if body, err = a.readBody(cmd, body, bodyFile); err != nil {
				return err
			}
			query := url.Values{"title": {title}, "body": {cmp.Or(body, templateBody)}}
			return a.openBrowser(cmd, repositoryURL(target)+"/issues/new?"+query.Encode())
		}
		if bodyFile == "-" && title == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--body-file - requires --title; stdin cannot also supply a prompt"))
		}
		if _, err := a.requireInput(cmd, &title, "title", "Title", nil); err != nil {
			return err
		}
		if target.client == nil {
			if target, err = a.resolve(cmd.Context(), true); err != nil {
				return err
			}
		}
		service := services.Issues{Client: target.client, Repository: target.Repo}
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
		var milestoneValue int64
		if milestone != "" {
			if milestoneValue, err = milestoneID(cmd, target, milestone); err != nil {
				return err
			}
		}
		labelIDs, err := (services.Labels{Client: target.client, Repository: target.Repo}).ResolveIssueNames(cmd.Context(), labelNames)
		if err != nil {
			return err
		}
		target.expandMeAll(assignees)
		item, err := service.Create(cmd.Context(), services.IssueOptions{Title: title, Body: body, Deadline: due, Ref: ref, Assignees: assignees, Milestone: milestoneValue, Labels: labelIDs})
		if err != nil {
			return err
		}
		var failures []error
		for _, file := range files {
			if _, err := service.UploadAttachment(cmd.Context(), item.Number, filepath.Base(file.Name()), file); err != nil {
				failures = append(failures, fmt.Errorf("issue %d created; attachment %s upload failed: %w", item.Number, filepath.Base(file.Name()), err))
			}
		}
		return errors.Join(append(failures, a.issueCreated(cmd, target, item))...)
	}}
	flags := cmd.Flags()
	flags.StringVarP(&title, "title", "t", "", "Issue title")
	flags.StringVarP(&body, "body", "b", "", "Issue body")
	flags.StringVarP(&bodyFile, "body-file", "F", "", "Read body from `file`, or - for stdin")
	flags.BoolVarP(&useEditor, "editor", "e", false, "Compose the issue body in the configured editor")
	flags.StringSliceVarP(&labelNames, "label", "l", nil, "Add labels by `name`; prefix repo: or org: to disambiguate")
	flags.StringSliceVarP(&assignees, "assignee", "a", nil, "Assign people by their `login`; @me for yourself")
	flags.StringVarP(&milestone, "milestone", "m", "", "Add the issue to a milestone by `title` or ID")
	flagAliases(cmd, map[string]string{"milestone-name": "milestone"})
	flags.StringVarP(&templateName, "template", "T", "", "Start from the repository issue template with this `name`")
	flags.StringVar(&ref, "ref", "", "Branch or tag the issue refers to")
	flags.StringVar(&deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339, or Unix timestamp")
	flags.StringArrayVar(&attachmentPaths, "attachment", nil, "`File` to attach after creation (repeatable)")
	flags.BoolVarP(&web, "web", "w", false, "Continue in the browser with the new-issue page")
	_ = cmd.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = cmd.RegisterFlagCompletionFunc("assignee", a.completeUsers())
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	return cmd
}

// issueCreated reports a new issue: as JSON, or its URL on stdout after a ✓ line for people.
func (a *application) issueCreated(cmd *cobra.Command, t target, item services.Issue) error {
	a.done(cmd, "Created issue %s", refLabel(t.Repo, item.Number, item.Title))
	return a.created(cmd, item, item.URL)
}

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
	choices := terminal.IssueFormChoices{CanPush: selectables.CanPush, Assignees: selectables.Assignees}
	// Without an editor the body is a field of the form, asked for right after the title.
	if preferences, err := a.preferences(); err != nil || preferences.Editor != "" {
		choices.EditBody = a.bodyEditor(cmd)
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
	item, err := service.Create(cmd.Context(), services.IssueOptions{
		Title: values.Title, Body: values.Body, Assignees: values.Assignees, Milestone: values.Milestone, Labels: values.Labels, Deadline: values.Deadline,
	})
	if err != nil {
		return err
	}
	return a.issueCreated(cmd, target, item)
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

// issueStateCommand closes or reopens issues, reporting those already in that state instead of
// changing them.
func (a *application) issueStateCommand(verb string) *cobra.Command {
	var comment string
	state, past, completeState, short := "closed", "Closed", "open", "Close issues"
	if verb == "reopen" {
		state, past, completeState, short = "open", "Reopened", "closed", "Reopen issues"
	}
	cmd := &cobra.Command{Use: verb + " {<number> | <url>}...", Short: short, Example: fmt.Sprintf(`$ gt issue %[1]s 12
$ gt issue %[1]s 12 13 --comment "Done in #14"`, verb), Annotations: map[string]string{"gt.arguments": issueArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		target, numbers, err := a.resolveRefs(cmd, args, issueRef)
		if err != nil {
			return err
		}
		service := services.Issues{Client: target.client, Repository: target.Repo}
		comments := services.Comments{Client: target.client, Repository: target.Repo}
		results, failures := applyBatch(cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
			current, err := service.View(cmd.Context(), n)
			if err != nil {
				return current, itemError(err, target.Repo)
			}
			if current.State == state || (state == "closed" && current.State == "merged") {
				a.warn(cmd, "%s %s is already %s", titleCase(discussionNoun(current.Kind)), refLabel(target.Repo, n, current.Title), current.State)
				return current, nil
			}
			if comment != "" {
				if _, err := comments.Create(cmd.Context(), n, comment); err != nil {
					return current, itemError(err, target.Repo)
				}
			}
			item, err := service.SetState(cmd.Context(), n, state)
			if err != nil {
				return item, itemError(err, target.Repo)
			}
			a.done(cmd, "%s %s %s", past, discussionNoun(item.Kind), refLabel(target.Repo, n, item.Title))
			return item, nil
		}, nil)
		return errors.Join(append(failures, writeBatchJSON(a, cmd, len(numbers), results))...)
	}}
	cmd.Flags().StringVarP(&comment, "comment", "c", "", "Leave a comment before the issue is "+state)
	cmd.ValidArgsFunction = a.completeIssues(completeState)
	return cmd
}

// discussionNoun names an issue of kind in messages: "issue" or "pull request".
func discussionNoun(kind string) string {
	if kind == services.KindPull {
		return "pull request"
	}
	return "issue"
}

// milestoneFlag reads -m/--milestone, a milestone title or ID, before any request: value is
// "" when the flag is absent, and remove reports 0 or --remove-milestone, which clear it.
func milestoneFlag(cmd *cobra.Command, value string) (selected string, remove bool, err error) {
	remove, _ = cmd.Flags().GetBool("remove-milestone")
	switch {
	case !cmd.Flags().Changed("milestone"):
		return "", remove, nil
	case remove:
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--milestone and --remove-milestone cannot be combined"))
	case strings.TrimSpace(value) == "":
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--milestone needs a title or ID; use --remove-milestone to clear it"))
	case strings.TrimSpace(value) == "0":
		return "", true, nil
	}
	return strings.TrimSpace(value), false, nil
}

// milestoneID resolves a milestone title or ID in the target repository.
func milestoneID(cmd *cobra.Command, t target, raw string) (int64, error) {
	milestone, err := (services.Milestones{Client: t.client, Repository: t.Repo}).FindOne(cmd.Context(), raw, false)
	return milestone.ID, err
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

// itemError words the failure of one item of a batch, which the batch prefixes with the item, such
// as "issue 9: not found in alice/x".
func itemError(err error, scope string) error {
	if services.IsNotFound(err) {
		return explain(err, "not found in %s", scope)
	}
	return err
}

// nameFilter splits a comma-separated filter of kind, trimming entries and rejecting blanks.
func nameFilter(kind, raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	names := strings.Split(raw, ",")
	return names, requireNames(kind, "", names)
}

// issueFilters checks the label and milestone filters of an issue listing in the target repository
// and returns them as query values. Gitea ignores labels and milestones it cannot find, and lists
// every issue when no filter value is left, so unknown names are errors.
func issueFilters(cmd *cobra.Command, t target, labels, milestones []string) (labelQuery, milestoneQuery string, err error) {
	if len(labels) > 0 {
		if err := (services.Labels{Client: t.client, Repository: t.Repo}).RequireFilterNames(cmd.Context(), labels); err != nil {
			return "", "", err
		}
	}
	var ids []string
	if len(milestones) > 0 {
		if ids, err = (services.Milestones{Client: t.client, Repository: t.Repo}).FilterIDs(cmd.Context(), milestones); err != nil {
			return "", "", err
		}
	}
	return strings.Join(labels, ","), strings.Join(ids, ","), nil
}

// metadataFlags are the label and assignee flags shared by issue and pull request edits.
type metadataFlags struct {
	setLabels, addLabels, removeLabels          []string
	setAssignees, addAssignees, removeAssignees []string
}

// register adds the flags under their gh names, --add-label and so on, and keeps the plural
// names working.
func (f *metadataFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringSliceVar(&f.addLabels, "add-label", nil, "Add labels by `name`; prefix repo: or org: to disambiguate")
	flags.StringSliceVar(&f.removeLabels, "remove-label", nil, "Remove labels by `name`")
	flags.StringSliceVar(&f.setLabels, "set-labels", nil, "Replace all labels; an empty value clears them")
	flags.StringSliceVar(&f.addAssignees, "add-assignee", nil, "Add assignees by `login`; @me for yourself")
	flags.StringSliceVar(&f.removeAssignees, "remove-assignee", nil, "Remove assignees by `login`; @me for yourself")
	flags.StringSliceVar(&f.setAssignees, "set-assignees", nil, "Replace all assignees; an empty value clears them")
}

// completeMetadataFlags completes the label and assignee flags of metadataFlags.
func (a *application) completeMetadataFlags(cmd *cobra.Command) {
	for _, name := range []string{"add-label", "remove-label", "set-labels"} {
		_ = cmd.RegisterFlagCompletionFunc(name, a.completeLabels())
	}
	for _, name := range []string{"add-assignee", "remove-assignee", "set-assignees"} {
		_ = cmd.RegisterFlagCompletionFunc(name, a.completeUsers())
	}
}

func (f *metadataFlags) validate(cmd *cobra.Command) error {
	for _, kind := range []string{"assignee", "label"} {
		if cmd.Flags().Changed("set-"+kind+"s") && (cmd.Flags().Changed("add-"+kind) || cmd.Flags().Changed("remove-"+kind)) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--set-%[1]ss cannot be combined with --add-%[1]s or --remove-%[1]s", kind))
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
// merges with each item's current state. @me names the account gt acts as.
func (f *metadataFlags) resolve(cmd *cobra.Command, t target) (labels *[]int64, assignees *[]string, changes *services.MetadataChanges, err error) {
	t.expandMeAll(f.setAssignees)
	t.expandMeAll(f.addAssignees)
	t.expandMeAll(f.removeAssignees)
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
