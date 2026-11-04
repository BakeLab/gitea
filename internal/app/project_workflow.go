// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

const projectWorkflowArguments = `A project can be supplied by its title or ID, as in gt project view; --by-name matches a number
as a title only. A workflow is supplied by its ID, which gt project workflow list shows.`

// workflowProject finds the project that a workflow command addresses.
type workflowProject func(cmd *cobra.Command, ref string) (services.ProjectBoards, services.Project, error)

var (
	workflowFilterFlags = []string{"issue-type", "from-column", "to-column", "label"}
	workflowActionFlags = []string{"column", "add-label", "remove-label", "issue-state"}
)

func (a *application) projectWorkflowCommand(scope projectScope, complete projectCompletion) *cobra.Command {
	group := &cobra.Command{Use: "workflow", Short: "Manage project workflows", Long: `Work with the workflows of a project: automations that move, label, close or reopen the issues and
pull requests of a project board when something happens to them. Workflows are addressed by ID.`}
	var byName bool
	group.PersistentFlags().BoolVar(&byName, "by-name", false, "Match a numeric project argument as a title rather than an ID")
	target := func(cmd *cobra.Command, ref string) (services.ProjectBoards, services.Project, error) {
		s, err := scope(cmd)
		if err != nil {
			return s, services.Project{}, err
		}
		found, err := s.Find(cmd.Context(), []string{ref}, byName)
		if err != nil {
			return s, services.Project{}, err
		}
		return s, found[0], nil
	}
	group.AddCommand(a.projectWorkflowListCommand(target), a.projectWorkflowViewCommand(target), a.projectWorkflowCreateCommand(target), a.projectWorkflowEditCommand(target),
		a.projectWorkflowStateCommand(true, target), a.projectWorkflowStateCommand(false, target), a.projectWorkflowDeleteCommand(target))
	for _, cmd := range group.Commands() {
		cmd.ValidArgsFunction = firstArg(complete("all"))
	}
	return jsonOutput[services.ProjectWorkflow](group)
}

func anyChanged(cmd *cobra.Command, names ...string) bool {
	return slices.ContainsFunc(names, cmd.Flags().Changed)
}

// workflowNames names the columns and labels that workflows refer to by ID.
type workflowNames struct {
	columns map[int64]string
	labels  services.IssueLabelCatalog
}

// orID is name, or #id when it is unknown.
func orID(name string, id int64) string {
	if name == "" {
		return "#" + strconv.FormatInt(id, 10)
	}
	return name
}

func (n workflowNames) list(ids []int64) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		name, _ := n.labels.Name(id)
		names[i] = orID(name, id)
	}
	return strings.Join(names, ", ")
}

func (n workflowNames) column(id int64) string { return orID(n.columns[id], id) }

// loadWorkflowNames reads, for the human views, the names of the columns and labels that items
// refer to; JSON carries IDs. Names it cannot read are shown as IDs.
func (a *application) loadWorkflowNames(cmd *cobra.Command, s services.ProjectBoards, project services.Project, items ...services.ProjectWorkflow) workflowNames {
	names := workflowNames{columns: map[int64]string{}}
	if a.options.json != "" {
		return names
	}
	columns := slices.ContainsFunc(items, func(item services.ProjectWorkflow) bool {
		return item.Filters.SourceColumnID != 0 || item.Filters.TargetColumnID != 0 || item.Actions.ColumnID != 0
	})
	labels := slices.ContainsFunc(items, func(item services.ProjectWorkflow) bool {
		return len(item.Filters.LabelIDs)+len(item.Actions.AddLabelIDs)+len(item.Actions.RemoveLabelIDs) > 0
	})
	if columns {
		if found, err := s.Columns(cmd.Context(), project); err == nil {
			for _, column := range found {
				names.columns[column.ID] = column.Title
			}
		}
	}
	if labels {
		names.labels, _ = s.LabelCatalog(cmd.Context())
	}
	return names
}

// workflowWhen describes the filters of a workflow: "pull requests, labeled bug".
func workflowWhen(item services.ProjectWorkflow, n workflowNames) string {
	var parts []string
	switch item.Filters.IssueType {
	case "issue":
		parts = append(parts, "issues")
	case "pull_request":
		parts = append(parts, "pull requests")
	}
	if item.Filters.SourceColumnID != 0 {
		parts = append(parts, "from "+n.column(item.Filters.SourceColumnID))
	}
	if item.Filters.TargetColumnID != 0 {
		parts = append(parts, "to "+n.column(item.Filters.TargetColumnID))
	}
	if len(item.Filters.LabelIDs) > 0 {
		parts = append(parts, "labeled "+n.list(item.Filters.LabelIDs))
	}
	if len(parts) == 0 {
		return "any item"
	}
	return strings.Join(parts, ", ")
}

// workflowDoes describes the actions of a workflow: "move to Done, add label bug, close".
func workflowDoes(item services.ProjectWorkflow, n workflowNames) string {
	var parts []string
	if item.Actions.ColumnID != 0 {
		parts = append(parts, "move to "+n.column(item.Actions.ColumnID))
	}
	if len(item.Actions.AddLabelIDs) > 0 {
		parts = append(parts, "add label "+n.list(item.Actions.AddLabelIDs))
	}
	if len(item.Actions.RemoveLabelIDs) > 0 {
		parts = append(parts, "remove label "+n.list(item.Actions.RemoveLabelIDs))
	}
	if item.Actions.IssueState != "" {
		parts = append(parts, item.Actions.IssueState)
	}
	return strings.Join(parts, ", ")
}

// projectWorkflowState is the state cell of a workflow: "enabled" or a muted "disabled".
func projectWorkflowState(item services.ProjectWorkflow) terminal.Cell {
	if item.Enabled {
		return terminal.Cell{Text: "enabled", Color: "green"}
	}
	return terminal.Cell{Text: "disabled", Muted: true}
}

func (a *application) projectWorkflowListCommand(target workflowProject) *cobra.Command {
	var limit, page int
	var all bool
	cmd := &cobra.Command{Use: "list <project>", Short: "List the workflows of a project", Long: "List the workflows of a project, hiding disabled ones by default.", Example: `$ gt project workflow list Roadmap
$ gt project workflow list 3 --all --org acme
$ gt project workflow list Roadmap --json id,event,enabled`, Annotations: map[string]string{"gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.ExactArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		items, err := s.Workflows(cmd.Context(), project)
		if err != nil {
			return err
		}
		if !all {
			items = slices.DeleteFunc(items, func(item services.ProjectWorkflow) bool { return !item.Enabled })
		}
		window, err := services.PageOf(items, limit, page)
		if err != nil {
			return err
		}
		names := a.loadWorkflowNames(cmd, s, project, window.Items...)
		what := "workflows in project " + strconv.Quote(project.Title)
		return writeTable(a, cmd, window.Items, listSpec[services.ProjectWorkflow]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Event"}, {Header: "State", PipedOnly: !all}, {Header: "When", Flex: true}, {Header: "Then", Flex: true}},
			Row: func(item services.ProjectWorkflow) []terminal.Cell {
				return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10)}, {Text: item.Event}, projectWorkflowState(item), {Text: workflowWhen(item, names)}, {Text: workflowDoes(item, names)}}
			},
			Empty: "no " + what, Context: what,
		}, servicePage(window, page))
	}}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include disabled workflows")
	return cmd
}

func (a *application) projectWorkflowViewCommand(target workflowProject) *cobra.Command {
	cmd := &cobra.Command{Use: "view <project> <workflow>", Short: "View a project workflow", Example: `$ gt project workflow view Roadmap 7
$ gt project workflow view 3 7 --org acme --json event,filters,actions`, Annotations: map[string]string{"gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.ExactArgs(2), "project title or ID", "workflow ID"), RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := resourceNumbers("workflow", args[1:])
		if err != nil {
			return err
		}
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		item, err := s.Workflow(cmd.Context(), project, ids[0])
		if err != nil {
			return err
		}
		names := a.loadWorkflowNames(cmd, s, project, item)
		return writeView(a, cmd, item, func(item services.ProjectWorkflow) terminal.View {
			state := projectWorkflowState(item)
			return terminal.View{
				Title: item.Event, ID: "#" + strconv.FormatInt(item.ID, 10), Bodyless: true, Kind: "workflow",
				Badges: []terminal.Cell{{Text: titleCase(state.Text), Color: state.Color, Muted: state.Muted}},
				Fields: []terminal.Field{
					{Label: "Project", Value: project.Title},
					{Label: "When", Value: workflowWhen(item, names)},
					{Label: "Then", Value: workflowDoes(item, names)},
					{Label: "State", Value: state.Text, PipedOnly: true},
				},
			}
		})
	}}
	return cmd
}

// workflowFlags are the settings of a workflow: when it runs, for which items and what it does.
type workflowFlags struct {
	event, issueType, fromColumn, toColumn, column, issueState string
	labels, addLabels, removeLabels                            []string
	disabled                                                   bool
}

func (f *workflowFlags) register(cmd *cobra.Command, creating bool) {
	flags := cmd.Flags()
	if creating {
		enumFlag(cmd, &f.event, "event", "", "", "Event that runs the workflow", services.ProjectWorkflowEvents...)
		flags.BoolVar(&f.disabled, "disabled", false, "Create the workflow disabled")
	}
	enumFlag(cmd, &f.issueType, "issue-type", "", "", "Run only for issues or pull requests", "issue", "pull_request", "any")
	flags.StringVar(&f.fromColumn, "from-column", "", "Run only for items moved from this `column`, by title or ID (event item_column_changed)")
	flags.StringVar(&f.toColumn, "to-column", "", "Run only for items moved to this `column`, by title or ID (event item_column_changed)")
	flags.StringSliceVarP(&f.labels, "label", "l", nil, "Run only for items with all of these label `names`")
	flags.StringVar(&f.column, "column", "", "Move the item to this `column`, by title or ID")
	flags.StringSliceVar(&f.addLabels, "add-label", nil, "Add these label `names` to the item")
	flags.StringSliceVar(&f.removeLabels, "remove-label", nil, "Remove these label `names` from the item")
	enumFlag(cmd, &f.issueState, "issue-state", "", "", "Close or reopen the item", "close", "reopen", "none")
}

// hasAction reports whether a creation flag asks the workflow to do something.
func (f *workflowFlags) hasAction() bool {
	return strings.TrimSpace(f.column) != "" || len(f.addLabels) > 0 || len(f.removeLabels) > 0 || f.issueState == "close" || f.issueState == "reopen"
}

// validate checks the label flags before any request.
func (f *workflowFlags) validate() error {
	if err := requireNames("label", "", f.labels, f.addLabels, f.removeLabels); err != nil {
		return err
	}
	return refuseAddedAndRemoved("label", f.addLabels, f.removeLabels)
}

// resolve returns the filters and actions to send, built on those of current, or nil for the part
// that no flag changed. Columns and labels are looked up once, and only when a flag names them.
func (f *workflowFlags) resolve(cmd *cobra.Command, s services.ProjectBoards, project services.Project, current services.ProjectWorkflow) (*services.ProjectWorkflowFilters, *services.ProjectWorkflowActions, error) {
	var columns []services.ProjectColumn
	column := func(raw string) (int64, error) {
		if strings.TrimSpace(raw) == "" {
			return 0, nil
		}
		if columns == nil {
			var err error
			if columns, err = s.Columns(cmd.Context(), project); err != nil {
				return 0, err
			}
		}
		found, err := services.FindColumn(columns, raw)
		return found.ID, err
	}
	var catalog *services.IssueLabelCatalog
	labels := func(names []string) ([]int64, error) {
		if len(names) == 0 {
			return nil, nil
		}
		if catalog == nil {
			loaded, err := s.LabelCatalog(cmd.Context())
			if err != nil {
				return nil, err
			}
			catalog = &loaded
		}
		return catalog.ResolveOrID(names)
	}
	filters, actions := current.Filters, current.Actions
	var err error
	set := func(flag string, apply func() error) {
		if err == nil && cmd.Flags().Changed(flag) {
			err = apply()
		}
	}
	set("issue-type", func() error { filters.IssueType = strings.TrimSuffix(f.issueType, "any"); return nil })
	set("from-column", func() (err error) { filters.SourceColumnID, err = column(f.fromColumn); return err })
	set("to-column", func() (err error) { filters.TargetColumnID, err = column(f.toColumn); return err })
	set("label", func() (err error) { filters.LabelIDs, err = labels(f.labels); return err })
	set("column", func() (err error) { actions.ColumnID, err = column(f.column); return err })
	set("add-label", func() (err error) { actions.AddLabelIDs, err = labels(f.addLabels); return err })
	set("remove-label", func() (err error) { actions.RemoveLabelIDs, err = labels(f.removeLabels); return err })
	set("issue-state", func() error { actions.IssueState = strings.TrimSuffix(f.issueState, "none"); return nil })
	if err != nil {
		return nil, nil, err
	}
	var changedFilters *services.ProjectWorkflowFilters
	var changedActions *services.ProjectWorkflowActions
	if anyChanged(cmd, workflowFilterFlags...) {
		changedFilters = &filters
	}
	if anyChanged(cmd, workflowActionFlags...) {
		changedActions = &actions
	}
	return changedFilters, changedActions, nil
}

func workflowLabel(s services.ProjectBoards, project services.Project, item services.ProjectWorkflow) string {
	return fmt.Sprintf("workflow %d (%s) of project %q in %s", item.ID, item.Event, project.Title, s.Scope())
}

func (a *application) projectWorkflowCreateCommand(target workflowProject) *cobra.Command {
	var f workflowFlags
	cmd := &cobra.Command{Use: "create <project>", Aliases: []string{"new"}, Short: "Create a project workflow", Long: `Create a workflow that runs on an event and does something to the issue or pull request
it concerns. --event and at least one action are required; the filters narrow which items it
runs for, and Gitea refuses those the event does not support. Columns are given by title or ID,
labels by name or ID.`, Example: `# move newly added items to the Backlog column
$ gt project workflow create Roadmap --event item_added_to_project --column Backlog
# move merged pull requests to Done and label them
$ gt project workflow create Roadmap --event pull_request_merged --column Done --add-label merged
# react to items moved from Doing to Done
$ gt project workflow create 3 --org acme --event item_column_changed --from-column Doing --to-column Done --issue-state close`, Annotations: map[string]string{"gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.ExactArgs(1), "project title or ID"), RunE: func(cmd *cobra.Command, args []string) error {
		if f.event == "" {
			return clierr.Wrap(clierr.Usage, errors.New("workflow creation requires --event"))
		}
		if f.event != "item_column_changed" && anyChanged(cmd, "from-column", "to-column") {
			return clierr.Wrap(clierr.Usage, errors.New("--from-column and --to-column require --event item_column_changed"))
		}
		if !f.hasAction() {
			return clierr.Wrap(clierr.Usage, errors.New("workflow creation requires an action: --column, --add-label, --remove-label or --issue-state"))
		}
		if err := f.validate(); err != nil {
			return err
		}
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		filters, actions, err := f.resolve(cmd, s, project, services.ProjectWorkflow{})
		if err != nil {
			return err
		}
		enabled := !f.disabled
		item, err := s.CreateWorkflow(cmd.Context(), project, services.ProjectWorkflowOptions{Event: f.event, Enabled: &enabled, Filters: filters, Actions: actions})
		if err != nil {
			return err
		}
		a.done(cmd, "Created %s", workflowLabel(s, project, item))
		return a.jsonResult(cmd, item)
	}}
	f.register(cmd, true)
	return cmd
}

func (a *application) projectWorkflowEditCommand(target workflowProject) *cobra.Command {
	var f workflowFlags
	cmd := &cobra.Command{Use: "edit <project> <workflow>", Short: "Edit a project workflow", Long: `Change the filters or actions of a workflow. Only the settings whose flags are passed change;
a list flag such as --label replaces the whole list, and an empty value clears it. The event
cannot be changed. Use gt project workflow enable or disable to switch a workflow on or off.`, Example: `$ gt project workflow edit Roadmap 7 --column Done
$ gt project workflow edit 3 7 --org acme --issue-type pull_request --label ""
$ gt project workflow edit Roadmap 7 --issue-state none`, Annotations: map[string]string{"gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.ExactArgs(2), "project title or ID", "workflow ID"), RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := resourceNumbers("workflow", args[1:])
		if err != nil {
			return err
		}
		if !anyChanged(cmd, slices.Concat(workflowFilterFlags, workflowActionFlags)...) {
			return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --column or --label (see --help)"))
		}
		if err := f.validate(); err != nil {
			return err
		}
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		current, err := s.Workflow(cmd.Context(), project, ids[0])
		if err != nil {
			return err
		}
		filters, actions, err := f.resolve(cmd, s, project, current)
		if err != nil {
			return err
		}
		item, err := s.EditWorkflow(cmd.Context(), project, ids[0], services.ProjectWorkflowOptions{Filters: filters, Actions: actions})
		if err != nil {
			return err
		}
		a.done(cmd, "Edited %s", workflowLabel(s, project, item))
		return a.jsonResult(cmd, item)
	}}
	f.register(cmd, false)
	return cmd
}

// projectWorkflowStateCommand enables or disables workflows, reporting those already in that state.
func (a *application) projectWorkflowStateCommand(enable bool, target workflowProject) *cobra.Command {
	verb, short, past := "disable", "Disable project workflows", "Disabled"
	if enable {
		verb, short, past = "enable", "Enable project workflows", "Enabled"
	}
	return &cobra.Command{Use: verb + " <project> <workflow>...", Short: short, Example: fmt.Sprintf(`$ gt project workflow %[1]s Roadmap 7
$ gt project workflow %[1]s 3 7 8 --org acme`, verb), Annotations: map[string]string{"gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.MinimumNArgs(2), "project title or ID", "workflow ID"), RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := resourceNumbers("workflow", args[1:])
		if err != nil {
			return err
		}
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		results, failures := applyBatch(cmd, ids, func(id int64) string { return "workflow " + strconv.FormatInt(id, 10) }, func(id int64) (services.ProjectWorkflow, error) {
			current, err := s.Workflow(cmd.Context(), project, id)
			if err != nil {
				return current, err
			}
			if current.Enabled == enable {
				a.warn(cmd, "%s is already %sd", titleCase(workflowLabel(s, project, current)), verb)
				return current, nil
			}
			item, err := s.EditWorkflow(cmd.Context(), project, id, services.ProjectWorkflowOptions{Enabled: &enable})
			if err == nil {
				a.done(cmd, "%s %s", past, workflowLabel(s, project, item))
			}
			return item, err
		}, nil)
		return errors.Join(append(failures, writeBatchJSON(a, cmd, len(ids), results))...)
	}}
}

func (a *application) projectWorkflowDeleteCommand(target workflowProject) *cobra.Command {
	return &cobra.Command{Use: "delete <project> <workflow>...", Short: "Delete project workflows", Example: `$ gt project workflow delete Roadmap 7
$ gt project workflow delete 3 7 8 --org acme --yes`, Annotations: map[string]string{"gt.text": "workflow deletion does not return a resource", "gt.confirm": "true", "gt.arguments": projectWorkflowArguments}, Args: nonBlankArgs(cobra.MinimumNArgs(2), "project title or ID", "workflow ID"), RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := resourceNumbers("workflow", args[1:])
		if err != nil {
			return err
		}
		s, project, err := target(cmd, args[0])
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("workflow %d", ids[0])
		if len(ids) > 1 {
			subject = fmt.Sprintf("%d workflows", len(ids))
		}
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Delete %s from project %q in %s?", subject, project.Title, s.Scope())}); err != nil {
			return err
		}
		return batchDone(a, cmd, ids, func(id int64) string { return "workflow " + strconv.FormatInt(id, 10) }, func(id int64) (int64, error) {
			return id, s.DeleteWorkflow(cmd.Context(), project, id)
		}, func(id int64) string {
			return fmt.Sprintf("Deleted workflow %d from project %q in %s", id, project.Title, s.Scope())
		})
	}}
}
