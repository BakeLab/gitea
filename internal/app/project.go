// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

const projectArguments = `A project can be supplied by its title, such as "Roadmap", or by its ID. A number is an ID
first, then a title; --by-name matches it as a title only.`

var (
	projectTemplates = []string{"none", "basic_kanban", "bug_triage"}
	projectCardTypes = []string{"text_only", "images_and_text"}
)

// projectScope returns the projects a project command manages: the repository's, or those of
// --org or --account.
type projectScope func(*cobra.Command) (services.ProjectBoards, error)

// projectCompletion completes the projects in state of the scope a project command manages.
type projectCompletion func(state string) cobra.CompletionFunc

func (a *application) projectCommand() *cobra.Command {
	group := &cobra.Command{Use: "project", Short: "Manage projects", Long: `Work with the project boards of a repository, of an organization with --org, or of your
account with --account. Projects are addressed by title or ID.`}
	org, resolve := a.orgScope(group, "projects")
	var account bool
	group.PersistentFlags().BoolVar(&account, "account", false, "Manage the projects of your account instead of a repository")
	scope := func(cmd *cobra.Command) (services.ProjectBoards, error) {
		if account {
			if conflict := setFlags([]string{"--org", "--repo"}, *org != "", a.options.repo != ""); len(conflict) > 0 {
				return services.ProjectBoards{}, clierr.Wrap(clierr.Usage, fmt.Errorf("--account cannot be combined with %s", conflict[0]))
			}
			t, err := a.resolve(cmd.Context(), false)
			return services.ProjectBoards{Client: t.client, Account: true}, err
		}
		t, err := resolve(cmd)
		return services.ProjectBoards{Client: t.client, Repository: t.Repo, Organization: *org}, err
	}
	complete := func(state string) cobra.CompletionFunc { return a.completeScopedProjects(org, &account, state) }
	group.AddCommand(a.projectListCommand(scope), a.projectViewCommand(scope, complete), a.projectCreateCommand(scope), a.projectEditCommand(scope, complete),
		a.projectStateCommand("close", scope, complete), a.projectStateCommand("reopen", scope, complete), a.projectDeleteCommand(scope, complete))
	return jsonOutput[services.Project](group)
}

func (a *application) projectListCommand(scope projectScope) *cobra.Command {
	var limit, page int
	var state string
	cmd := &cobra.Command{Use: "list", Short: "List projects", Example: `$ gt project list
$ gt project list --org acme --state all
$ gt project list --json id,title,open_issues`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		items, err := s.ListPage(cmd.Context(), state, limit, page)
		if err != nil {
			return err
		}
		what := "projects in " + s.Scope()
		if state != "all" {
			what = state + " " + what
		}
		return writeTable(a, cmd, items.Items, listSpec[services.Project]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Title", Flex: true}, {Header: "State", PipedOnly: state == "open"}, {Header: "Progress"}},
			Row:     projectRow, Empty: "no " + what, Context: what,
		}, servicePage(items, page))
	}}
	pageFlags(cmd, &limit, &page)
	enumFlag(cmd, &state, "state", "s", "open", "Filter by state", "open", "closed", "all")
	return cmd
}

func projectRow(item services.Project) []terminal.Cell {
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10), Color: stateColors[item.State]},
		{Text: item.Title},
		{Text: item.State, Color: stateColors[item.State]},
		{Text: progress(item.OpenIssues, item.ClosedIssues)},
	}
}

// projectName names a project in messages: `project "Roadmap" in alice/x`.
func projectName(s services.ProjectBoards, item services.Project) string {
	return fmt.Sprintf("project %q in %s", item.Title, s.Scope())
}

func projectView(item services.Project) terminal.View {
	return terminal.View{
		Title: item.Title, ID: "#" + strconv.FormatInt(item.ID, 10),
		Badges: []terminal.Cell{stateBadge(item.State)},
		Meta:   fmt.Sprintf("%s • %d open, %d closed", item.Type, item.OpenIssues, item.ClosedIssues),
		Fields: []terminal.Field{
			{Label: "State", Value: item.State, PipedOnly: true},
			{Label: "Type", Value: item.Type, PipedOnly: true},
			{Label: "Card type", Value: item.CardType, PipedOnly: true},
			{Label: "Progress", Value: progress(item.OpenIssues, item.ClosedIssues), PipedOnly: true},
		},
		Body: item.Description, Kind: "project", URL: item.URL,
	}
}

func (a *application) projectViewCommand(scope projectScope, complete projectCompletion) *cobra.Command {
	var byName, web bool
	cmd := &cobra.Command{Use: "view <project>", Short: "View a project", Example: `$ gt project view Roadmap
$ gt project view 3 --web --org acme`, Annotations: map[string]string{"gt.arguments": projectArguments}, Args: nonBlankArgs(cobra.ExactArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		found, err := s.Find(cmd.Context(), args, byName)
		if err != nil {
			return err
		}
		if web {
			return a.openBrowser(cmd, found[0].URL)
		}
		return writeView(a, cmd, found[0], projectView)
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a project title rather than an ID")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the project in the browser")
	cmd.ValidArgsFunction = firstArg(complete("all"))
	return cmd
}

type projectFields struct {
	title, description, cardType, template string
}

func (f *projectFields) register(cmd *cobra.Command, creating bool) {
	flags := cmd.Flags()
	flags.StringVarP(&f.title, "title", "t", "", "Project title")
	flags.StringVarP(&f.description, "description", "d", "", "Project description")
	enumFlag(cmd, &f.cardType, "card-type", "", "", "How cards are rendered", projectCardTypes...)
	if creating {
		enumFlag(cmd, &f.template, "template", "", "", "Board template of a new project, creating its columns", projectTemplates...)
	}
}

// options returns the fields that were passed, checking them before any request.
func (f *projectFields) options(cmd *cobra.Command) (services.ProjectOptions, error) {
	var opts services.ProjectOptions
	if cmd.Flags().Changed("title") || f.title != "" {
		if f.title = strings.TrimSpace(f.title); f.title == "" {
			return opts, clierr.Wrap(clierr.Usage, errors.New("project title cannot be empty"))
		}
		opts.Title = &f.title
	}
	if cmd.Flags().Changed("description") {
		opts.Description = &f.description
	}
	if cmd.Flags().Changed("card-type") {
		opts.CardType = &f.cardType
	}
	opts.Template = f.template
	return opts, nil
}

func (a *application) projectCreateCommand(scope projectScope) *cobra.Command {
	var fields projectFields
	var force bool
	cmd := &cobra.Command{Use: "create [<title>]", Aliases: []string{"new"}, Short: "Create a project", Long: `Create a project and print its URL. Without a title on a terminal, a prompt asks for it. A
project with the same title, ignoring case, is refused unless --force updates it, since titles
address projects.`, Example: `$ gt project create Roadmap --template basic_kanban
$ gt project create --title "Bug triage" --org acme --template bug_triage --description "Incoming bugs"
# Update the project if it exists
$ gt project create Roadmap --description "Planned work" --force`, Args: nonBlankArgs(cobra.MaximumNArgs(1), "project title"), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			if cmd.Flags().Changed("title") {
				return clierr.Wrap(clierr.Usage, errors.New("a title argument cannot be combined with --title"))
			}
			if err := cmd.Flags().Set("title", args[0]); err != nil {
				return err
			}
		}
		if _, err := a.requireInput(cmd, &fields.title, "title", "Project title", nil); err != nil {
			return err
		}
		opts, err := fields.options(cmd)
		if err != nil {
			return err
		}
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		existing, err := s.All(cmd.Context())
		if err != nil {
			return err
		}
		item, updated, err := s.CreateUnique(cmd.Context(), existing, opts, force)
		if err != nil {
			return forceHint(err)
		}
		verb := "Created"
		if updated {
			verb = "Updated"
		}
		a.done(cmd, "%s %s", verb, projectName(s, item))
		return a.created(cmd, item, item.URL)
	}}
	fields.register(cmd, true)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Update the project if one with the same title exists")
	return cmd
}

func (a *application) projectEditCommand(scope projectScope, complete projectCompletion) *cobra.Command {
	var fields projectFields
	var byName bool
	cmd := &cobra.Command{Use: "edit <project>", Short: "Edit a project", Example: `$ gt project edit Roadmap --title "Roadmap 2026"
$ gt project edit 3 --description "Planned work" --card-type images_and_text`, Annotations: map[string]string{"gt.arguments": projectArguments}, Args: nonBlankArgs(cobra.ExactArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := fields.options(cmd)
		if err != nil {
			return err
		}
		if opts.Title == nil && opts.Description == nil && opts.CardType == nil {
			return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title or --description (see --help)"))
		}
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		// One listing finds the project and checks a new title.
		existing, err := s.All(cmd.Context())
		if err != nil {
			return err
		}
		current, err := s.FindIn(existing, args[0], byName)
		if err != nil {
			return err
		}
		item, err := s.EditUnique(cmd.Context(), existing, current, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Edited %s", projectName(s, item))
		return a.jsonResult(cmd, item)
	}}
	fields.register(cmd, false)
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a project title rather than an ID")
	cmd.ValidArgsFunction = firstArg(complete("all"))
	return cmd
}

// selectProjects finds the projects that args address, refusing one that is selected twice.
func selectProjects(cmd *cobra.Command, s services.ProjectBoards, args []string, byName bool) ([]services.Project, error) {
	found, err := s.Find(cmd.Context(), args, byName)
	if err != nil {
		return nil, err
	}
	return found, refuseRepeats("project", args, found, func(p services.Project) int64 { return p.ID })
}

// projectLabel names a project in batch failures: `project "Roadmap"`.
func projectLabel(item services.Project) string { return "project " + strconv.Quote(item.Title) }

// projectStateCommand closes or reopens projects, reporting those already in that state instead of
// changing them.
func (a *application) projectStateCommand(verb string, scope projectScope, complete projectCompletion) *cobra.Command {
	var byName bool
	state, other, past, short := "closed", "open", "Closed", "Close projects"
	if verb == "reopen" {
		state, other, past, short = "open", "closed", "Reopened", "Reopen projects"
	}
	cmd := &cobra.Command{Use: verb + " <project>...", Short: short, Example: fmt.Sprintf(`$ gt project %[1]s Roadmap
$ gt project %[1]s 3 4 --org acme`, verb), Annotations: map[string]string{"gt.arguments": projectArguments}, Args: nonBlankArgs(cobra.MinimumNArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		selected, err := selectProjects(cmd, s, args, byName)
		if err != nil {
			return err
		}
		results, failures := applyBatch(cmd, selected, projectLabel, func(project services.Project) (services.Project, error) {
			if project.State == state {
				a.warn(cmd, "%s is already %s", titleCase(projectName(s, project)), state)
				return project, nil
			}
			item, err := s.Edit(cmd.Context(), project.ID, services.ProjectOptions{State: &state})
			if err == nil {
				a.done(cmd, "%s %s", past, projectName(s, item))
			}
			return item, err
		}, nil)
		return errors.Join(append(failures, writeBatchJSON(a, cmd, len(selected), results))...)
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match numeric arguments as project titles rather than IDs")
	// Only projects that are not in the target state are worth completing.
	cmd.ValidArgsFunction = complete(other)
	return cmd
}

func (a *application) projectDeleteCommand(scope projectScope, complete projectCompletion) *cobra.Command {
	var byName bool
	cmd := &cobra.Command{Use: "delete <project>...", Short: "Delete projects", Example: `$ gt project delete Roadmap
$ gt project delete 3 4 --org acme --yes`, Annotations: map[string]string{"gt.text": "project deletion does not return a resource", "gt.confirm": "true", "gt.arguments": projectArguments}, Args: nonBlankArgs(cobra.MinimumNArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		found, err := selectProjects(cmd, s, args, byName)
		if err != nil {
			return err
		}
		subject := selectionSubject("project", found, func(p services.Project) string { return p.Title })
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + " from " + s.Scope() + "?"}); err != nil {
			return err
		}
		return batchDone(a, cmd, found, projectLabel, func(item services.Project) (services.Project, error) {
			return item, s.Delete(cmd.Context(), item.ID)
		}, func(item services.Project) string {
			return fmt.Sprintf("Deleted project %q from %s", item.Title, s.Scope())
		})
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match numeric arguments as project titles rather than IDs")
	cmd.ValidArgsFunction = complete("all")
	return cmd
}

// completeScopedProjects completes the projects in state of the repository or of --org.
func (a *application) completeScopedProjects(org *string, account *bool, state string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return a.apiCompletion(*org == "" && !*account, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			items, err := (services.ProjectBoards{Client: t.client, Repository: t.Repo, Organization: *org, Account: *account}).ListPage(ctx, state, completionLimit, 1)
			return describe(items.Items, func(item services.Project) (string, string) {
				return titleCandidate(item.ID, item.Title, item.Description)
			}), err
		})(cmd, args, toComplete)
	}
}
