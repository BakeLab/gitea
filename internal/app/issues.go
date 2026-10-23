// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

const issueArguments = `An issue can be supplied as argument in any of the following formats:
- by number, e.g. "123" or "#123"
- by URL, e.g. "https://gitea.com/OWNER/REPO/issues/123"
- by repository and number, e.g. "OWNER/REPO#123"`

func (a *application) issueCommand() *cobra.Command {
	group := &cobra.Command{Use: "issue", Short: "Manage issues", Long: `Work with the issues of a repository: list, view and create them, edit, close or reopen
one or more, and comment on them. Gitea lists pull requests among issues, so most issue
commands also accept a pull request number.`}
	group.AddCommand(a.issueListCommand(), a.issueViewCommand(), a.issueCreateCommand(), a.issueEditCommand(),
		a.issueStateCommand("close"), a.issueStateCommand("reopen"), a.issueAttachmentCommand(issueRef), a.commentCommand(issueRef), a.timelineCommand(issueRef))
	return jsonOutput(group, jsonFields[services.Issue]())
}

func (a *application) issueListCommand() *cobra.Command {
	opts := services.ListOptions{Page: 1}
	var labels []string
	var updated timeRange
	var web bool
	cmd := &cobra.Command{Use: "list", Short: "List issues in a repository", Long: `List the issues of a repository, newest first. Filters combine: an issue must match all of them.
Labels and milestones must exist in the repository.`, Example: `$ gt issue list --label bug --state all
$ gt issue list --assignee @me --json number,title
$ gt issue list --no-assignee --label bug
# Issues and pull requests in a milestone, as tab-separated values
$ gt issue list --kind all --milestone v1.0 | cut -f1,2`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		var err error
		if opts.Since, opts.Before, err = updated.parse(a.deps.Now()); err != nil {
			return err
		}
		if opts.NoAssignee && opts.Assignee != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--assignee cannot be combined with --no-assignee"))
		}
		if err := requireNames("label", "", labels); err != nil {
			return err
		}
		milestones, err := nameFilter("milestone", opts.Milestones)
		if err != nil {
			return err
		}
		if web {
			if err := requireWebFlags(cmd, "state", "kind", "search", "no-assignee"); err != nil {
				return err
			}
			if opts.Kind == "all" {
				return clierr.Wrap(clierr.Usage, errors.New("--kind all cannot be combined with --web; the browser lists issues or pull requests, not both"))
			}
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
		filtered := opts.Search+opts.Assignee+opts.Author+opts.Mentions != "" || len(labels)+len(milestones) > 0 || opts.Since != nil || opts.Before != nil || opts.NoAssignee
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
	enumFlag(cmd, &opts.Kind, "kind", "", "issues", "List issues, pull requests or both; the kind field of each reads issue or pull", "issues", "pulls", "all")
	pageFlags(cmd, &opts.Limit, &opts.Page)
	flags := cmd.Flags()
	flags.StringVarP(&opts.Search, "search", "S", "", "Search issue titles and bodies")
	flags.StringVarP(&opts.Assignee, "assignee", "a", "", "Filter by assignee; @me for yourself")
	flags.BoolVar(&opts.NoAssignee, "no-assignee", false, "Only issues without an assignee")
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
// the --search text and limited to unassigned ones for --no-assignee.
func issuesURL(t target, opts services.ListOptions) string {
	section := "issues"
	if opts.Kind == "pulls" {
		section = "pulls"
	}
	query := url.Values{"state": {opts.State}}
	if opts.Search != "" {
		query.Set("q", opts.Search)
	}
	if opts.NoAssignee {
		query.Set("assignee", "(none)")
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
conversation follows the body. The blocked_by and blocks JSON fields cost extra requests, made
only when the output includes them (see gt help formatting).`, Example: `$ gt issue view 12
$ gt issue view 12 --comments
$ gt issue view https://gitea.com/OWNER/REPO/issues/12 --web`, Annotations: map[string]string{"gt.arguments": issueArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if comments && a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--comments cannot be combined with --json or --jq; use gt issue comment list <number> --json"))
		}
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
		if a.options.json != "" && !a.jsonSelects("blocked_by", "blocks") {
			return a.writeJSON(cmd, issueDetails{Issue: item})
		}
		dependencies, err := service.Dependencies(cmd.Context(), n)
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, issueDetails{Issue: item, BlockedBy: dependencies.BlockedBy, Blocks: dependencies.Blocks})
		}
		return a.writeDiscussion(cmd, target, issueView(item, target.Repo, dependencies), comments)
	}}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the issue in the browser")
	cmd.Flags().BoolVarP(&comments, "comments", "c", false, "View issue comments")
	cmd.ValidArgsFunction = firstArg(a.completeIssues("open"))
	return jsonOutput(cmd, jsonFields[issueDetails]())
}

// issueDetails is the JSON of issue view: the issue with the issues it depends on and blocks,
// which are read only when the output includes them: all fields, as --json alone and --jq output,
// or a --json naming blocked_by or blocks.
type issueDetails struct {
	services.Issue
	BlockedBy []services.IssueReference `json:"blocked_by,omitzero"`
	Blocks    []services.IssueReference `json:"blocks,omitzero"`
}

func (a *application) issueCreateCommand() *cobra.Command {
	var title, body, bodyFile, deadline, templateName, ref, milestone string
	var useEditor, web bool
	var assignees, labelNames, attachmentPaths, projects []string
	cmd := &cobra.Command{Use: "create", Aliases: []string{"new"}, Short: "Create an issue", Long: `Create an issue in a repository. Without flags on a terminal, a form asks for the title and
body, then optionally for assignees, a milestone, labels and a due date.

Gitea sets labels, a milestone, assignees and a due date only for people who can write to the
issues of the repository, and creates the issue without them for others: gt then names the
flags or form fields it left out and fails, and warns about left-out labels of a template.
Adding the issue to a project needs write access too; when that fails, the issue stays created
and the error names the command that adds it later.

--template starts from a repository issue template: its title, body, labels and ref. --title,
--label, --body, --body-file and --ref replace them; labels the repository lacks are left out.
A form (YAML) template starts with an empty body. On a terminal the form opens with the title
and body, and a template picked in the form applies its labels too. The title of a template is
a prefix to complete, so without the form --template requires --title. --web opens the
new-issue page with the whole template applied.`, Example: `$ gt issue create
$ gt issue create --title "Crash on start" --body "Steps to reproduce…" --label bug --milestone v1.0
$ gt issue create --template "Bug report"`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
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
		if err := requireNames("project", "", projects); err != nil {
			return err
		}
		files, err := openRegularFiles(attachmentPaths)
		defer closeFiles(files)
		if err != nil {
			return err
		}
		var due *time.Time
		if cmd.Flags().Changed("deadline") {
			if due, err = parseDeadline(deadline); err != nil {
				return err
			}
		}
		if web {
			// The new-issue page is prefilled with the title and body only.
			if err := onlyFlagsWith(cmd, "web", "the browser only receives the title and body", "title", "body", "body-file", "template"); err != nil {
				return err
			}
		}
		if !localFlagsChanged(cmd, "template") && a.canPrompt() && a.deps.IssueForm != nil {
			return a.createIssueInteractively(cmd, strings.TrimSpace(templateName))
		}
		var target target
		var templateBody, templateFile string
		var templateLabels []string
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
			// --title and --label replace the title and labels of the template, as --body replaces its body.
			// The title of a template is a prefix to complete, such as "[Bug]: ", so it only prefills
			// the browser page; without a form, --title is required.
			templateBody, templateFile = issueTemplateBody(template), template.FileName
			if !cmd.Flags().Changed("title") && web && strings.TrimSpace(template.Title) != "" {
				title = template.Title
			}
			if !cmd.Flags().Changed("label") {
				templateLabels = template.Labels
			}
			if !cmd.Flags().Changed("ref") {
				ref = template.Ref
			}
		}
		if web {
			if body, err = a.readBody(cmd, body, bodyFile); err != nil {
				return err
			}
			query := url.Values{"title": {title}, "body": {cmp.Or(body, templateBody)}}
			if templateFile != "" {
				// The page applies the whole template, its labels, assignees and ref too; the
				// title and body given here replace the template's.
				query = url.Values{"template": {templateFile}}
				if cmd.Flags().Changed("title") {
					query.Set("title", title)
				}
				if body != "" {
					query.Set("body", body)
				}
			}
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
		labelService := services.Labels{Client: target.client, Repository: target.Repo}
		labelIDs, err := labelService.ResolveIssueNames(cmd.Context(), labelNames)
		if err != nil {
			return err
		}
		requested := issueMetadata{labels: labelIDs, milestone: milestoneValue, deadline: due != nil}
		if labelIDs, err = a.addTemplateLabels(cmd, labelService, labelIDs, templateLabels); err != nil {
			return err
		}
		// Projects are resolved first, so that a missing one fails before the issue exists.
		boards, err := loadProjectChanges(cmd.Context(), target, projects, nil, false)
		if err != nil {
			return err
		}
		target.expandMeAll(assignees)
		item, err := service.Create(cmd.Context(), services.IssueOptions{Title: title, Body: body, Deadline: due, Ref: ref, Assignees: assignees, Milestone: milestoneValue, Labels: labelIDs})
		if err != nil {
			return err
		}
		requested.assignees = assignees
		a.warnTemplateLabelsLeftOut(cmd, target, item, requested, labelIDs)
		failures := uploadAttachments(cmd.Context(), files, item.Number, service.UploadAttachment, nil)
		if _, err := boards.apply(cmd.Context(), item.ID); err != nil {
			failures = append(failures, fmt.Errorf("%w; add it later with: gt issue edit %d %s%s", err, item.Number, projectFlags(boards.add), a.suggestedRepo(target)))
		}
		return errors.Join(requested.leftOut(item, target.Repo, issueMetadataFlags), followUpError(fmt.Sprintf("issue #%d", item.Number), "created", failures), a.issueCreated(cmd, target, item))
	}}
	flags := cmd.Flags()
	flags.StringVarP(&title, "title", "t", "", "Issue title")
	flags.StringVarP(&body, "body", "b", "", "Issue body")
	flags.StringVarP(&bodyFile, "body-file", "F", "", "Read body from `file`, or - for stdin")
	flags.BoolVarP(&useEditor, "editor", "e", false, "Compose the issue body in the configured editor")
	flags.StringSliceVarP(&labelNames, "label", "l", nil, "Add labels by `name`; prefix repo: or org: to disambiguate")
	flags.StringSliceVarP(&assignees, "assignee", "a", nil, "Assign people by their `login`; @me for yourself")
	flags.StringVarP(&milestone, "milestone", "m", "", "Add the issue to a milestone by `title` or ID")
	flags.StringVarP(&templateName, "template", "T", "", "Start from the repository issue template with this `name`")
	flags.StringVar(&ref, "ref", "", "Branch or tag the issue refers to")
	flags.StringVar(&deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339 or Unix timestamp; only the date counts, as the end of that day in UTC")
	flags.StringArrayVar(&attachmentPaths, "attachment", nil, "`File` to attach after creation (repeatable)")
	flags.StringArrayVarP(&projects, "project", "p", nil, "Add the issue to the repository's or its owner's project with this ID or `title`, a number being an ID first (repeatable)")
	flags.BoolVarP(&web, "web", "w", false, "Continue in the browser with the new-issue page")
	_ = cmd.RegisterFlagCompletionFunc("label", a.completeLabels())
	_ = cmd.RegisterFlagCompletionFunc("assignee", a.completeUsers())
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	return cmd
}

// issueMetadata is what issue create asks Gitea to set besides the title and body.
type issueMetadata struct {
	labels    []int64
	milestone int64
	assignees []string
	deadline  bool
}

// issueMetadataNames name the labels, milestone, assignees and due date the way they were asked for.
type issueMetadataNames struct{ labels, milestone, assignees, deadline string }

var (
	issueMetadataFlags  = issueMetadataNames{labels: "--label", milestone: "--milestone", assignees: "--assignee", deadline: "--deadline"}
	issueMetadataFields = issueMetadataNames{labels: "labels", milestone: "milestone", assignees: "assignees", deadline: "due date"}
)

// leftOut fails when the created item lacks metadata that was asked for, named by names. Gitea
// drops the labels, milestone, assignees and due date of people who cannot write to the issues of
// repository without an error, and the permissions of a repository tell only about its code, not
// its issues.
func (m issueMetadata) leftOut(item services.Issue, repository string, names issueMetadataNames) error {
	var missing []string
	if slices.ContainsFunc(m.labels, func(id int64) bool { return !hasLabel(item, id) }) {
		missing = append(missing, names.labels)
	}
	if m.milestone != 0 && (item.Milestone == nil || item.Milestone.ID != m.milestone) {
		missing = append(missing, names.milestone)
	}
	if slices.ContainsFunc(m.assignees, func(login string) bool {
		return !slices.ContainsFunc(item.Assignees, func(assignee string) bool { return strings.EqualFold(assignee, login) })
	}) {
		missing = append(missing, names.assignees)
	}
	if m.deadline && item.Deadline == nil {
		missing = append(missing, names.deadline)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("issue #%d was created without %s: Gitea leaves them out for people who cannot write to the issues of %s", item.Number, strings.Join(missing, ", "), repository)
}

// warnTemplateLabelsLeftOut warns when the item lacks labels of the issue template, the labels
// beyond those requested, which Gitea leaves out like the requested ones.
func (a *application) warnTemplateLabelsLeftOut(cmd *cobra.Command, t target, item services.Issue, requested issueMetadata, labels []int64) {
	if slices.ContainsFunc(labels, func(id int64) bool { return !slices.Contains(requested.labels, id) && !hasLabel(item, id) }) {
		a.warn(cmd, "Gitea left out the labels of the issue template, as you cannot write to the issues of %s", t.Repo)
	}
}

// hasLabel reports whether item has the label with id.
func hasLabel(item services.Issue, id int64) bool {
	return slices.ContainsFunc(item.Labels, func(label services.Label) bool { return label.ID == id })
}

// projectFlags are the issue edit flags that add an issue to boards, by ID as titles can be
// ambiguous.
func projectFlags(boards []services.Board) string {
	flags := make([]string, len(boards))
	for i, board := range boards {
		flags[i] = "--add-project " + strconv.FormatInt(board.ID, 10)
	}
	return strings.Join(flags, " ")
}

// issueCreated reports a new issue: as JSON, or its URL on stdout after a ✓ line for people.
func (a *application) issueCreated(cmd *cobra.Command, t target, item services.Issue) error {
	a.done(cmd, "Created issue %s", refLabel(t.Repo, item.Number, item.Title))
	return a.created(cmd, item, item.URL)
}

// createIssueInteractively runs the create form; a non-empty templateName pre-fills its title and body.
func (a *application) createIssueInteractively(cmd *cobra.Command, templateName string) (runErr error) {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	service := services.Issues{Client: target.client, Repository: target.Repo}
	selectables, err := service.CreateSelectables(cmd.Context())
	if err != nil {
		return err
	}
	choices := terminal.IssueFormChoices{Assignees: selectables.Assignees}
	// Without an editor the body is a field of the form, asked for right after the title.
	if preferences, err := a.preferences(); err != nil || preferences.Editor != "" {
		choices.EditBody = a.bodyEditor(cmd, terminal.BodyTitle)
	}
	for _, template := range selectables.Templates {
		choices.Templates = append(choices.Templates, issueTemplateChoice(template))
	}
	choices.Milestones, choices.Labels = milestoneChoices(selectables.Milestones), labelChoices(selectables.Labels)
	if templateName != "" {
		template, err := issueTemplate(cmd.Context(), service, templateName)
		if err != nil {
			return err
		}
		choice := issueTemplateChoice(template)
		choices.Template = &choice
	}
	values, err := a.deps.IssueForm.CreateIssue(cmd.Context(), choices)
	defer finishDraft(&values.Draft, &runErr)
	if err != nil {
		return err
	}
	created := false
	defer func() {
		// A body typed into the form has no draft of the editor; keep it when no issue was created.
		if runErr != nil && !created && values.Draft.Path == "" {
			values.Draft = keepDraft(values.Body)
		}
	}()
	requested := issueMetadata{labels: values.Labels, milestone: values.Milestone, assignees: values.Assignees, deadline: values.Deadline != nil}
	labelIDs, err := a.addTemplateLabels(cmd, services.Labels{Client: target.client, Repository: target.Repo}, values.Labels, values.TemplateLabels)
	if err != nil {
		return err
	}
	if values.Deadline != nil {
		due := endOfDay(*values.Deadline)
		values.Deadline = &due
	}
	item, err := service.Create(cmd.Context(), services.IssueOptions{
		Title: values.Title, Body: values.Body, Ref: values.TemplateRef, Assignees: values.Assignees, Milestone: values.Milestone, Labels: labelIDs, Deadline: values.Deadline,
	})
	if err != nil {
		return err
	}
	created = true
	a.warnTemplateLabelsLeftOut(cmd, target, item, requested, labelIDs)
	return errors.Join(requested.leftOut(item, target.Repo, issueMetadataFields), a.issueCreated(cmd, target, item))
}

// issueTemplateChoice offers template in the create form, which applies its title, body, labels and ref.
func issueTemplateChoice(template services.IssueTemplate) terminal.IssueTemplateChoice {
	return terminal.IssueTemplateChoice{Name: template.Name, About: template.About, Title: template.Title, Body: issueTemplateBody(template), Ref: template.Ref, Labels: template.Labels}
}

// issueTemplateBody seeds a body from markdown content. A form template has no content, and its
// description is no body, so it seeds none.
func issueTemplateBody(template services.IssueTemplate) string {
	return template.Content
}

// addTemplateLabels adds the labels an issue template names to ids. Like Gitea's new-issue page,
// it skips labels the repository lacks, with a warning, and picks the repository's label over the
// organization's of the same name.
func (a *application) addTemplateLabels(cmd *cobra.Command, service services.Labels, ids []int64, names []string) ([]int64, error) {
	names = slices.DeleteFunc(slices.Clone(names), func(name string) bool { return strings.TrimSpace(name) == "" })
	if len(names) == 0 {
		return ids, nil
	}
	catalog, err := service.IssueCatalog(cmd.Context())
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		found, err := catalog.Resolve([]string{name})
		if _, ambiguous := errors.AsType[*services.AmbiguousError](err); ambiguous {
			found, err = catalog.Resolve([]string{"repo:" + name})
			if err != nil {
				// The template, not the user, named the label, so it is skipped rather than refused.
				a.warn(cmd, "label %q of the issue template matches several labels in %s; it was left out", name, service.Repository)
				continue
			}
		}
		if _, missing := errors.AsType[*services.NotFoundError](err); missing {
			a.warn(cmd, "label %q of the issue template does not exist in %s; it was left out", name, service.Repository)
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
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
	commentExample, commentUsage := "Done in #14", "Leave a comment before closing the issue"
	if verb == "reopen" {
		state, past, completeState, short = "open", "Reopened", "closed", "Reopen issues"
		commentExample, commentUsage = "Still happens on v1.2", "Leave a comment before reopening the issue"
	}
	cmd := &cobra.Command{Use: verb + " {<number> | <url>}...", Short: short, Example: fmt.Sprintf(`$ gt issue %[1]s 12
$ gt issue %[1]s 12 13 --comment %[2]q`, verb, commentExample), Annotations: map[string]string{"gt.arguments": issueArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
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
			if state == "open" && current.State == "merged" {
				// Gitea refuses the state change, so fail before the comment is left behind.
				return current, fmt.Errorf("pull request %s is already merged; it cannot be reopened", refLabel(target.Repo, n, current.Title))
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
	cmd.Flags().StringVarP(&comment, "comment", "c", "", commentUsage)
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
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--milestone cannot be combined with --remove-milestone"))
	case strings.TrimSpace(value) == "":
		hint := "omit --milestone for none"
		if cmd.Flags().Lookup("remove-milestone") != nil {
			hint = "use --remove-milestone to clear it"
		}
		return "", false, clierr.Wrap(clierr.Usage, errors.New("--milestone needs a title or ID; "+hint))
	case strings.TrimSpace(value) == "0":
		return "", true, nil
	}
	return strings.TrimSpace(value), false, nil
}

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
// as "issue 9: not found in alice/x". A missing repository keeps its own name.
func itemError(err error, scope string) error {
	if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Kind == "repository" {
		return err
	}
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

// register adds the flags under their gh names, --add-label and so on.
func (f *metadataFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringSliceVar(&f.addLabels, "add-label", nil, "Add labels by `name`; prefix repo: or org: to disambiguate")
	flags.StringSliceVar(&f.removeLabels, "remove-label", nil, "Remove labels by `name`")
	flags.StringSliceVar(&f.setLabels, "set-labels", nil, "Replace all labels; an empty value clears them")
	flags.StringSliceVar(&f.addAssignees, "add-assignee", nil, "Add assignees by `login`; @me for yourself")
	flags.StringSliceVar(&f.removeAssignees, "remove-assignee", nil, "Remove assignees by `login`; @me for yourself")
	flags.StringSliceVar(&f.setAssignees, "set-assignees", nil, "Replace all assignees; an empty value clears them")
}

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
	// The repository's and its organization's labels are loaded once for all label flags.
	var catalog *services.IssueLabelCatalog
	resolveLabels := func(names []string) ([]int64, error) {
		if len(names) == 0 {
			return []int64{}, nil
		}
		if catalog == nil {
			loaded, err := (services.Labels{Client: t.client, Repository: t.Repo}).IssueCatalog(cmd.Context())
			if err != nil {
				return nil, err
			}
			catalog = &loaded
		}
		return catalog.Resolve(names)
	}
	if cmd.Flags().Changed("set-labels") {
		ids, err := resolveLabels(f.setLabels)
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
	if changes.AddLabels, err = resolveLabels(f.addLabels); err != nil {
		return nil, nil, nil, err
	}
	if changes.RemoveLabels, err = resolveLabels(f.removeLabels); err != nil {
		return nil, nil, nil, err
	}
	return labels, assignees, changes, nil
}
