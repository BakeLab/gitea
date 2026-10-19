// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

const milestoneArguments = `A milestone can be supplied by its title, such as "v1.0", or by its ID. A number is an ID
first, then a title; --by-name matches it as a title only.`

// milestoneService returns the milestones of the repository a milestone command targets.
type milestoneService func(*cobra.Command) (services.Milestones, target, error)

func (a *application) milestoneCommand() *cobra.Command {
	group := &cobra.Command{Use: "milestone", Short: "Manage milestones", Long: `Work with the milestones of a repository and the issues and pull requests in them.
Milestones are addressed by title or ID.`}
	service := func(cmd *cobra.Command) (services.Milestones, target, error) {
		t, err := a.resolve(cmd.Context(), true)
		return services.Milestones{Client: t.client, Repository: t.Repo}, t, err
	}
	group.AddCommand(a.milestoneListCommand(service), a.milestoneViewCommand(service), a.milestoneCreateCommand(service), a.milestoneEditCommand(service),
		a.milestoneDeleteCommand(service), a.milestoneStateCommand("close", service), a.milestoneStateCommand("reopen", service), a.milestoneIssueCommand())
	return group
}

func (a *application) milestoneListCommand(service milestoneService) *cobra.Command {
	var limit, page int
	var state string
	cmd := &cobra.Command{Use: "list", Short: "List milestones", Long: "List the milestones of a repository: open ones first, then by due date.", Example: `$ gt milestone list
$ gt milestone list --state all --json title,due_on,open_issues,closed_issues`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, t, err := service(cmd)
		if err != nil {
			return err
		}
		// Gitea pages milestones by due date alone, so the order is applied to all of them first.
		all, err := s.AllIn(cmd.Context(), state)
		if err != nil {
			return err
		}
		slices.SortStableFunc(all, compareMilestones)
		start := len(all)
		if page-1 <= len(all)/limit {
			start = min((page-1)*limit, len(all))
		}
		items := services.Page[services.Milestone]{Items: all[start:min(start+limit, len(all))], More: start+limit < len(all), Total: len(all), Counted: true}
		what := "milestones in " + t.Repo
		if state != "all" {
			what = state + " " + what
		}
		return writeTable(a, cmd, items.Items, listSpec[services.Milestone]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Title", Flex: true}, {Header: "State", PipedOnly: state == "open"}, {Header: "Due"}, {Header: "Progress"}},
			Row:     milestoneRow, Empty: "no " + what, Context: what,
		}, servicePage(items, page))
	}}
	pageFlags(cmd, &limit, &page)
	enumFlag(cmd, &state, "state", "s", "open", "Filter by state", "open", "closed", "all")
	return cmd
}

// compareMilestones orders open milestones first, then by due date, those without one last.
func compareMilestones(x, y services.Milestone) int {
	if x.State != y.State {
		return cmp.Compare(boolOrder(x.State != "open"), boolOrder(y.State != "open"))
	}
	if x.Deadline == nil || y.Deadline == nil {
		return cmp.Compare(boolOrder(x.Deadline == nil), boolOrder(y.Deadline == nil))
	}
	return terminal.ParseTime(*x.Deadline).Compare(terminal.ParseTime(*y.Deadline))
}

func boolOrder(b bool) int {
	if b {
		return 1
	}
	return 0
}

func milestoneRow(item services.Milestone) []terminal.Cell {
	due := terminal.Cell{}
	if item.Deadline != nil {
		due.Time = terminal.ParseTime(*item.Deadline)
	}
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10), Color: stateColors[item.State]},
		{Text: item.Title},
		{Text: item.State, Color: stateColors[item.State]},
		due,
		{Text: milestoneProgress(item)},
	}
}

// milestoneProgress summarizes the closed issues of a milestone: "3/5 (60%)".
func milestoneProgress(item services.Milestone) string {
	total := item.OpenIssues + item.ClosedIssues
	if total == 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d (%d%%)", item.ClosedIssues, total, item.ClosedIssues*100/total)
}

// milestoneName names a milestone in messages: `milestone "v1.0" in alice/x`.
func milestoneName(t target, item services.Milestone) string {
	return fmt.Sprintf("milestone %q in %s", item.Title, t.Repo)
}

// milestoneURL is the web page of a milestone, which the API does not return.
func milestoneURL(t target, item services.Milestone) string {
	return repositoryURL(t) + "/milestone/" + strconv.FormatInt(item.ID, 10)
}

func milestoneView(t target, now time.Time) func(services.Milestone) terminal.View {
	return func(item services.Milestone) terminal.View {
		total := item.OpenIssues + item.ClosedIssues
		meta := []string{fmt.Sprintf("%d of %d closed", item.ClosedIssues, total)}
		if total > 0 {
			meta[0] += fmt.Sprintf(" (%d%%)", item.ClosedIssues*100/total)
		}
		due := ""
		if item.Deadline != nil {
			due = terminal.Timestamp(terminal.ParseTime(*item.Deadline))
			meta = append([]string{"due " + dueText(now, *item.Deadline)}, meta...)
		}
		return terminal.View{
			Title: item.Title, ID: "#" + strconv.FormatInt(item.ID, 10),
			Badges: []terminal.Cell{stateBadge(item.State)}, Meta: strings.Join(meta, " • "),
			Fields: []terminal.Field{
				{Label: "State", Value: item.State, PipedOnly: true},
				{Label: "Due", Value: due, PipedOnly: true},
				{Label: "Progress", Value: milestoneProgress(item), PipedOnly: true},
			},
			Body: item.Description, Kind: "milestone", URL: milestoneURL(t, item),
		}
	}
}

func (a *application) milestoneViewCommand(service milestoneService) *cobra.Command {
	var byName, web bool
	cmd := &cobra.Command{Use: "view <milestone>", Short: "View a milestone", Example: `$ gt milestone view v1.0
$ gt milestone view 3 --web`, Annotations: map[string]string{"gt.arguments": milestoneArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, t, err := service(cmd)
		if err != nil {
			return err
		}
		item, err := findMilestone(cmd, s, args[0], byName)
		if err != nil {
			return err
		}
		if web {
			return a.openBrowser(cmd, milestoneURL(t, item))
		}
		return writeView(a, cmd, item, milestoneView(t, a.deps.Now()))
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a milestone title rather than an ID")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the milestone in the browser")
	cmd.ValidArgsFunction = firstArg(a.completeMilestonesIn("all"))
	return cmd
}

type milestoneFields struct {
	title, description, descriptionFile, deadline string
	useEditor                                     bool
}

func (f *milestoneFields) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.title, "title", "t", "", "Milestone title")
	flags.StringVarP(&f.description, "description", "d", "", "Milestone description")
	flags.StringVarP(&f.descriptionFile, "description-file", "F", "", "Read the description from `file`, or - for stdin")
	flags.BoolVarP(&f.useEditor, "editor", "e", false, "Write the description in the configured editor")
	flags.StringVar(&f.deadline, "deadline", "", "Due `date`: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears it")
}

// milestoneOptions returns the fields that were passed, reading the description and checking the
// rest before any request. Callers run the editor for --editor.
func (a *application) milestoneOptions(cmd *cobra.Command, f *milestoneFields) (services.MilestoneOptions, error) {
	var opts services.MilestoneOptions
	if cmd.Flags().Changed("title") {
		if strings.TrimSpace(f.title) == "" {
			return opts, clierr.Wrap(clierr.Usage, errors.New("milestone title cannot be empty"))
		}
		opts.Title = &f.title
	}
	if cmd.Flags().Changed("deadline") {
		date := time.Time{}
		if f.deadline != "" {
			parsed, err := parseDeadline(f.deadline)
			if err != nil {
				return opts, err
			}
			date = *parsed
		}
		normalized := date.Format(time.RFC3339)
		opts.Deadline = &normalized
	}
	if cmd.Flags().Changed("description") || cmd.Flags().Changed("description-file") {
		description, err := a.readTextSource(cmd, f.description, f.descriptionFile, "description", "description-file")
		if err != nil {
			return opts, err
		}
		opts.Description = &description
	}
	return opts, nil
}

func formOptions(values terminal.MilestoneFormValues) services.MilestoneOptions {
	deadline := time.Time{}
	if values.Deadline != nil {
		deadline = *values.Deadline
	}
	normalized := deadline.Format(time.RFC3339)
	return services.MilestoneOptions{Title: &values.Title, Description: &values.Description, Deadline: &normalized}
}

func (a *application) milestoneCreateCommand(service milestoneService) *cobra.Command {
	var fields milestoneFields
	var state string
	cmd := &cobra.Command{Use: "create [<title>]", Aliases: []string{"new"}, Short: "Create a milestone", Long: `Create a milestone. Without flags on a terminal, a form asks for the title, description
and due date.`, Example: `$ gt milestone create v1.0 --deadline 2026-12-31 --description "First release"
$ gt milestone create --title v2.0 --editor`, Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		if err := validateTextSources(cmd, "description", "description-file"); err != nil {
			return err
		}
		if len(args) == 1 {
			if cmd.Flags().Changed("title") {
				return clierr.Wrap(clierr.Usage, errors.New("pass the title as an argument or --title, not both"))
			}
			if err := cmd.Flags().Set("title", args[0]); err != nil {
				return err
			}
		}
		var opts services.MilestoneOptions
		var draft terminal.Draft
		var err error
		defer finishDraft(&draft, &runErr)
		if !localFlagsChanged(cmd) && a.canPrompt() && a.deps.MilestoneForm != nil {
			values, err := a.deps.MilestoneForm.EditMilestone(cmd.Context(), "", terminal.MilestoneFormValues{Editor: a.editorCommand()})
			if err != nil {
				return err
			}
			opts = formOptions(values)
		} else {
			if opts, err = a.milestoneOptions(cmd, &fields); err != nil {
				return err
			}
			if _, err := a.requireInput(cmd, &fields.title, "title", "Milestone title", nil); err != nil {
				return err
			}
			opts.Title = &fields.title
			if fields.useEditor {
				if draft, err = a.editDraft(cmd, ""); err != nil {
					return err
				}
				opts.Description = &draft.Body
			}
		}
		opts.State = &state
		s, t, err := service(cmd)
		if err != nil {
			return err
		}
		item, err := s.Create(cmd.Context(), opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Created %s", milestoneName(t, item))
		return a.created(cmd, item, milestoneURL(t, item))
	}}
	fields.register(cmd)
	enumFlag(cmd, &state, "state", "", "open", "State of the new milestone", "open", "closed")
	return cmd
}

func (a *application) milestoneEditCommand(service milestoneService) *cobra.Command {
	var fields milestoneFields
	var byName bool
	cmd := &cobra.Command{Use: "edit <milestone>", Short: "Edit a milestone", Long: `Edit the title, description or due date of a milestone. Without flags on a terminal, a form
starts from the current values.`, Example: `$ gt milestone edit v1.0 --title v1.0.0 --deadline 2027-01-31
$ gt milestone edit v1.0 --deadline=
$ gt milestone edit 3 --editor`, Annotations: map[string]string{"gt.arguments": milestoneArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		if err := validateTextSources(cmd, "description", "description-file"); err != nil {
			return err
		}
		form := !localFlagsChanged(cmd) && a.canPrompt() && a.deps.MilestoneForm != nil
		if !form && !localFlagsChanged(cmd) {
			return errNothingToEdit
		}
		var opts services.MilestoneOptions
		var draft terminal.Draft
		var err error
		defer finishDraft(&draft, &runErr)
		if !form {
			if opts, err = a.milestoneOptions(cmd, &fields); err != nil || (opts == (services.MilestoneOptions{}) && !fields.useEditor) {
				return cmp.Or(err, errNothingToEdit)
			}
		}
		s, t, err := service(cmd)
		if err != nil {
			return err
		}
		current, err := findMilestone(cmd, s, args[0], byName)
		if err != nil {
			return err
		}
		switch {
		case form:
			values := terminal.MilestoneFormValues{Title: current.Title, Description: current.Description, Editor: a.editorCommand()}
			if current.Deadline != nil {
				deadline := terminal.ParseTime(*current.Deadline)
				values.Deadline = &deadline
			}
			if values, err = a.deps.MilestoneForm.EditMilestone(cmd.Context(), "Editing "+milestoneName(t, current), values); err != nil {
				return err
			}
			opts = formOptions(values)
		case fields.useEditor:
			if draft, err = a.editDraft(cmd, current.Description); err != nil {
				return err
			}
			opts.Description = &draft.Body
		}
		item, err := s.Edit(cmd.Context(), current.ID, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Edited %s", milestoneName(t, item))
		return a.jsonResult(cmd, item)
	}}
	fields.register(cmd)
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a milestone title rather than an ID")
	cmd.ValidArgsFunction = firstArg(a.completeMilestonesIn("all"))
	return cmd
}

func (a *application) milestoneDeleteCommand(service milestoneService) *cobra.Command {
	var byName bool
	cmd := &cobra.Command{Use: "delete <milestone>", Short: "Delete a milestone", Long: "Delete a milestone. Its issues and pull requests stay, without a milestone.", Example: `$ gt milestone delete v0.9
$ gt milestone delete 7 --yes`, Annotations: map[string]string{"gt.confirm": "true", "gt.arguments": milestoneArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, t, err := service(cmd)
		if err != nil {
			return err
		}
		item, err := findMilestone(cmd, s, args[0], byName)
		if err != nil {
			return err
		}
		name := fmt.Sprintf("milestone %q (ID %d) from %s", item.Title, item.ID, t.Repo)
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + name + "?"}); err != nil {
			return err
		}
		if err := s.Delete(cmd.Context(), item.ID); err != nil {
			return err
		}
		a.done(cmd, "Deleted milestone %q from %s", item.Title, t.Repo)
		return a.jsonResult(cmd, item)
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a milestone title rather than an ID")
	cmd.ValidArgsFunction = firstArg(a.completeMilestonesIn("all"))
	return cmd
}

func findMilestone(cmd *cobra.Command, s services.Milestones, raw string, byName bool) (services.Milestone, error) {
	if strings.TrimSpace(raw) == "" {
		return services.Milestone{}, clierr.Wrap(clierr.Usage, errors.New("milestone title or ID cannot be empty"))
	}
	return s.FindOne(cmd.Context(), raw, byName)
}
