// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
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

// workflowArguments describes the workflow references that workflow commands accept.
const workflowArguments = `A workflow can be supplied as its file name ("ci.yml"), its file name without
extension ("ci"), its path (".gitea/workflows/ci.yml") or its name ("CI").`

func (a *application) workflowCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "workflow",
		Short: "View, run, enable and disable Actions workflows",
		Long:  "Work with the Actions workflows of a repository. Inspect their runs with gt run.",
	}
	group.AddCommand(a.workflowListCommand(), a.workflowViewCommand(), a.workflowRunCommand(), a.workflowStateCommand(true), a.workflowStateCommand(false))
	return group
}

func (a *application) workflowListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the workflows of a repository",
		Example: `# list the workflows of the repository
$ gt workflow list
# list the files of disabled workflows
$ gt workflow list --json id,state --jq '.[] | select(.state != "active") | .id'`,
		Args: argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			items, err := (services.Actions{Client: t.client, Repository: t.Repo}).Workflows(cmd.Context())
			if err != nil {
				return err
			}
			spec := listSpec[services.Workflow]{Columns: workflowColumns, Row: workflowRow, Empty: "no workflows in " + t.Repo, Context: "workflows in " + t.Repo}
			return writeTable(a, cmd, items, spec, listPage{Number: 1, Total: len(items), Counted: true})
		},
	}
}

var workflowColumns = []terminal.Column{{Header: "Name", Flex: true}, {Header: "File"}, {Header: "State"}, {Header: "Path", PipedOnly: true}, {Header: "URL", PipedOnly: true}}

func workflowRow(item services.Workflow) []terminal.Cell {
	return []terminal.Cell{{Text: item.Name, Link: item.URL}, {Text: item.ID}, workflowState(item), {Text: item.Path}, {Text: item.URL}}
}

// workflowState shows a workflow's state, "disabled" for Gitea's disabled_manually.
func workflowState(item services.Workflow) terminal.Cell {
	if !item.Enabled() {
		return terminal.Cell{Text: "disabled", Muted: true}
	}
	return terminal.Cell{Text: item.State, Color: "green"}
}

func (a *application) workflowViewCommand() *cobra.Command {
	var showYAML, web bool
	var ref string
	cmd := &cobra.Command{
		Use:   "view <workflow>",
		Short: "View a workflow and its recent runs",
		Example: `# view a workflow and its recent runs
$ gt workflow view CI
# print the workflow file of a branch
$ gt workflow view ci.yml --yaml --ref feature/login
# open the workflow file in the browser
$ gt workflow view ci --web`,
		Args:        argsExact(1),
		Annotations: map[string]string{"gt.arguments": workflowArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if ref != "" && !showYAML {
				return clierr.Wrap(clierr.Usage, errors.New("--ref selects the version that --yaml prints"))
			}
			if showYAML && (web || a.options.json != "") {
				return clierr.Wrap(clierr.Usage, errors.New("--yaml cannot be combined with --web, --json or --jq"))
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			workflow, err := service.FindWorkflow(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			switch {
			case web:
				return a.openBrowser(cmd, workflow.URL)
			case showYAML:
				definition, err := service.WorkflowFile(cmd.Context(), workflow, ref)
				if err != nil {
					return err
				}
				return writeTerminalSafe(cmd.OutOrStdout(), a.deps.Streams.OutTTY, bytes.NewReader(definition))
			case a.options.json != "":
				return a.writeJSON(cmd, workflow)
			}
			runs, err := service.RunsPage(cmd.Context(), services.RunListOptions{Limit: 5, Page: 1, Workflow: workflow.ID})
			if err != nil {
				return err
			}
			return a.writeHuman(cmd, a.renderWorkflow(t, workflow, runs.Items))
		},
	}
	cmd.Flags().BoolVar(&showYAML, "yaml", false, "Print the workflow file")
	cmd.Flags().StringVarP(&ref, "ref", "r", "", "Branch, tag or commit of the workflow file that --yaml prints (default: the default branch)")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the workflow file in the browser")
	cmd.ValidArgsFunction = firstArg(a.completeWorkflows())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	return cmd
}

func (a *application) renderWorkflow(t target, workflow services.Workflow, runs []services.Run) string {
	d := a.display()
	rows := make([][]terminal.Cell, len(runs))
	for i, run := range runs {
		rows[i] = runRow(a.deps.Now())(run)
	}
	if !d.TTY {
		fields := []terminal.Field{{Label: "File", Value: workflow.ID}, {Label: "Path", Value: workflow.Path}, {Label: "State", Value: workflow.State}}
		return terminal.View{Title: workflow.Name, Fields: fields, URL: workflow.URL, Body: terminal.Table(runColumns, rows, d)}.Render(d)
	}
	muted := func(text string) string { return terminal.Cell{Text: text, Muted: true}.Render(d) }
	var b strings.Builder
	b.WriteString(terminal.Cell{Text: workflow.Name, Bold: true}.Render(d) + " " + muted(workflow.ID) + "\n")
	b.WriteString(workflowState(workflow).Render(d) + " • " + muted(workflow.Path) + "\n\n")
	b.WriteString(terminal.Cell{Text: "RECENT RUNS", Bold: true}.Render(d) + "\n")
	if len(runs) == 0 {
		b.WriteString(muted("No runs yet") + "\n")
	} else {
		b.WriteString(terminal.Table(runColumns, rows, d))
	}
	b.WriteString("\n" + muted(fmt.Sprintf("To see more runs: gt run list --workflow %s%s", workflow.ID, a.suggestedRepo(t))) + "\n")
	if workflow.URL != "" {
		b.WriteString(muted("View this workflow on Gitea: "+terminal.SafeLine(workflow.URL)) + "\n")
	}
	return b.String()
}

func (a *application) workflowRunCommand() *cobra.Command {
	var ref string
	var fields, inputs []string
	var follow bool
	var interval, timeout time.Duration
	cmd := &cobra.Command{
		Use:   "run <workflow>",
		Short: "Dispatch a workflow",
		Long: `Start a run of a workflow that has a workflow_dispatch trigger.

Inputs are checked against the inputs that the workflow declares: unknown inputs, missing
required inputs and values that a choice or boolean input does not accept are refused. In a
terminal, gt asks for missing required inputs, or for every input when none is given.

Without --ref, the workflow runs on the checked-out branch, which must be on the server, or on
the default branch when the repository does not come from the working directory.`,
		Example: `# run a workflow on the default or current branch
$ gt workflow run CI
# run it on a branch with inputs
$ gt workflow run ci.yml --ref main -f target=production -f dry_run=false
# run it and follow the logs of its jobs
$ gt workflow run nightly --follow`,
		Args:        argsExact(1),
		Annotations: map[string]string{"gt.arguments": workflowArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if follow && a.options.json != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--follow cannot be combined with --json/--jq"))
			}
			if interval <= 0 || timeout <= 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
			}
			values, err := inputValues(append(fields, inputs...))
			if err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			workflow, err := service.FindWorkflow(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !workflow.Enabled() {
				return fmt.Errorf("workflow %s is disabled; enable it with: gt workflow enable %s%s", workflow.ID, workflow.ID, a.suggestedRepo(t))
			}
			if ref, err = a.dispatchRef(cmd.Context(), t, ref); err != nil {
				return err
			}
			definition, err := service.WorkflowFile(cmd.Context(), workflow, ref)
			if err != nil {
				return err
			}
			declared, err := services.DispatchInputs(definition)
			if errors.Is(err, services.ErrNoDispatch) {
				return fmt.Errorf("%s cannot be dispatched: it has no workflow_dispatch trigger on %s", workflow.Label(), ref)
			}
			if err != nil {
				return err
			}
			if values, err = a.dispatchValues(cmd, workflow, declared, values, len(fields)+len(inputs) == 0); err != nil {
				return err
			}
			result, err := service.Dispatch(cmd.Context(), workflow.ID, ref, values)
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, result)
			}
			if result.ID == 0 {
				a.done(cmd, "Dispatched %s on %s", workflow.Label(), ref)
				if follow {
					return errors.New("the server did not report the run it started; cannot follow its logs")
				}
				return nil
			}
			a.done(cmd, "Dispatched %s on %s → run %d", workflow.Label(), ref, result.ID)
			if !follow {
				a.nextStep(cmd, "Watch it: gt run watch %d%s", result.ID, a.suggestedRepo(t))
			}
			if err := a.created(cmd, result, result.URL); err != nil || !follow {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			job, err := service.WaitForFirstJob(ctx, result.ID, interval)
			if err != nil {
				return timeoutError(err, timeout, "waiting for the jobs of run %d", result.ID)
			}
			jobs, err := service.Jobs(ctx, result.ID)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				jobs = []services.Job{job}
			}
			return timeoutError(a.followJobLogs(ctx, cmd, service, result.ID, jobs, interval), timeout, "following the logs of run %d", result.ID)
		},
	}
	cmd.Flags().StringVarP(&ref, "ref", "r", "", "Branch or tag to run the workflow on (default: the current or default branch)")
	cmd.Flags().StringArrayVarP(&fields, "field", "f", nil, "Workflow input as `key=value` (repeatable)")
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "Alias of --field")
	_ = cmd.Flags().MarkHidden("input")
	cmd.Flags().BoolVar(&follow, "follow", false, "Follow the logs of the run's jobs after dispatching it")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Polling interval with --follow")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum time to follow")
	cmd.ValidArgsFunction = firstArg(a.completeWorkflows())
	_ = cmd.RegisterFlagCompletionFunc("ref", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("field", a.completeWorkflowInputs())
	_ = cmd.RegisterFlagCompletionFunc("input", a.completeWorkflowInputs())
	return cmd
}

// nextStep shows a next step under a done line, under the same conditions.
func (a *application) nextStep(cmd *cobra.Command, format string, args ...any) {
	if a.deps.Streams.OutTTY && a.options.json == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "  "+terminal.Colorize("muted", terminal.SafeLine(fmt.Sprintf(format, args...)), a.colorEnabled()))
	}
}

func inputValues(fields []string) (map[string]string, error) {
	values := map[string]string{}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid input %q: use key=value", field))
		}
		if _, exists := values[key]; exists {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("input %s is given more than once", key))
		}
		values[key] = value
	}
	return values, nil
}

// dispatchRef returns the ref to dispatch a workflow on: ref when set, else the checked-out
// branch when the repository comes from the working directory and the branch is on the server,
// else the repository's default branch.
func (a *application) dispatchRef(ctx context.Context, t target, ref string) (string, error) {
	if ref != "" {
		return ref, nil
	}
	defaultBranch := func() (string, error) {
		repo, err := services.Repositories{Client: t.client}.View(ctx, t.Repo)
		return repo.DefaultBranch, err
	}
	branch, err := a.currentBranch(ctx)
	if err != nil || branch == "" || a.suggestedRepo(t) != "" {
		return defaultBranch()
	}
	_, err = services.Branches{Client: t.client}.View(ctx, t.Repo, branch)
	if services.IsNotFound(err) {
		fallback, err := defaultBranch()
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("branch %s is not on %s; push it or pass --ref %s", branch, t.Repo, fallback)
	}
	return branch, err
}

// dispatchValues checks the inputs given for a workflow against those it declares, and asks
// for the missing required ones, or for all of them with askAll, when prompts are possible.
func (a *application) dispatchValues(cmd *cobra.Command, workflow services.Workflow, declared []services.WorkflowInput, values map[string]string, askAll bool) (map[string]string, error) {
	names := make([]string, len(declared))
	for i, input := range declared {
		names[i] = input.Name
	}
	for key := range values {
		if slices.Contains(names, key) {
			continue
		}
		if len(names) == 0 {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("%s takes no inputs", workflow.Label()))
		}
		return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("unknown input %q for %s; inputs: %s", key, workflow.Label(), strings.Join(names, ", ")))
	}
	canPrompt := a.canPrompt()
	for _, input := range declared {
		value, given := values[input.Name]
		missing := input.Required && input.Default == ""
		switch {
		case given:
			normalized, err := inputValue(input, value)
			if err != nil {
				return nil, clierr.Wrap(clierr.Usage, err)
			}
			values[input.Name] = normalized
		case canPrompt && (askAll || missing):
			answer, err := a.askInput(cmd, input)
			if err != nil {
				return nil, err
			}
			values[input.Name] = answer
		case missing:
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("missing required input %s for %s; pass -f %s=<value>", input.Name, workflow.Label(), input.Name))
		}
	}
	return values, nil
}

// inputValue checks a value against its input's type: a choice must be one of its options, a
// boolean true or false, a number a number. It returns the value as the workflow expects it.
func inputValue(input services.WorkflowInput, value string) (string, error) {
	switch input.Type {
	case "choice":
		if !slices.Contains(input.Options, value) {
			return "", fmt.Errorf("invalid value %q for input %s: use %s", value, input.Name, strings.Join(input.Options, ", "))
		}
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("invalid value %q for input %s: use true or false", value, input.Name)
		}
		return strconv.FormatBool(parsed), nil
	case "number":
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return "", fmt.Errorf("invalid value %q for input %s: use a number", value, input.Name)
		}
	}
	if input.Required && value == "" {
		return "", fmt.Errorf("input %s is required", input.Name)
	}
	return value, nil
}

// askInput asks for one workflow input: a selection for a choice, yes or no for a boolean, and
// text otherwise, where an empty answer keeps the default.
func (a *application) askInput(cmd *cobra.Command, input services.WorkflowInput) (string, error) {
	title := input.Name
	if input.Description != "" {
		title += " — " + input.Description
	}
	switch chooser, ok := a.deps.Prompter.(terminal.Chooser); {
	case input.Type == "choice" && ok && len(input.Options) > 0:
		choices := make([]terminal.Choice, len(input.Options))
		for i, option := range input.Options {
			choices[i] = terminal.Choice{Label: option, Value: option}
		}
		return chooser.Choose(cmd.Context(), title, choices, input.Default)
	case input.Type == "boolean":
		yes, err := a.deps.Prompter.ConfirmDefault(cmd.Context(), title, input.Default == "true")
		return strconv.FormatBool(yes), err
	}
	if input.Default != "" {
		title += " (default " + input.Default + ")"
	}
	validate := func(answer string) error {
		if answer == "" {
			if input.Required && input.Default == "" {
				return errors.New("a value is required")
			}
			return nil
		}
		_, err := inputValue(input, answer)
		return err
	}
	answer, err := a.deps.Prompter.Ask(cmd.Context(), title, validate)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return input.Default, nil
	}
	return inputValue(input, answer)
}

func (a *application) workflowStateCommand(enable bool) *cobra.Command {
	verb, short, done := "disable", "Disable a workflow", "Disabled"
	if enable {
		verb, short, done = "enable", "Enable a workflow", "Enabled"
	}
	return &cobra.Command{
		Use:   verb + " <workflow>",
		Short: short,
		Example: fmt.Sprintf(`# %s a workflow by name
$ gt workflow %s Nightly`, verb, verb),
		Args:              argsExact(1),
		Annotations:       map[string]string{"gt.arguments": workflowArguments},
		ValidArgsFunction: firstArg(a.completeWorkflows()),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			workflow, err := service.FindWorkflow(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if workflow.Enabled() == enable {
				a.warn(cmd, "%s is already %sd", workflow.Label(), verb)
			} else {
				if err := service.SetWorkflowEnabled(cmd.Context(), workflow.ID, enable); err != nil {
					return err
				}
				if workflow, err = service.Workflow(cmd.Context(), workflow.ID); err != nil {
					return err
				}
				a.done(cmd, "%s %s", done, workflow.Label())
			}
			return a.jsonResult(cmd, workflow)
		},
	}
}

// completeWorkflowInputs completes the inputs of the workflow given as the first argument as
// name=, then the values of a choice or boolean input.
func (a *application) completeWorkflowInputs() cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		key, _, value := strings.Cut(toComplete, "=")
		values, directive := a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			service := services.Actions{Client: t.client, Repository: t.Repo}
			workflow, err := service.FindWorkflow(ctx, args[0])
			if err != nil {
				return nil, err
			}
			definition, err := service.WorkflowFile(ctx, workflow, "")
			if err != nil {
				return nil, err
			}
			declared, err := services.DispatchInputs(definition)
			var candidates [][2]string
			for _, input := range declared {
				switch {
				case !value:
					candidates = append(candidates, [2]string{input.Name + "=", input.Description})
				case input.Name != key:
				case input.Type == "boolean":
					candidates = append(candidates, [2]string{key + "=true"}, [2]string{key + "=false"})
				default:
					for _, option := range input.Options {
						candidates = append(candidates, [2]string{key + "=" + option})
					}
				}
			}
			return describe(candidates, func(candidate [2]string) (string, string) { return candidate[0], candidate[1] }), err
		})(cmd, args, toComplete)
		if !value {
			directive |= cobra.ShellCompDirectiveNoSpace
		}
		return values, directive
	}
}
