// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
)

// runArguments describes the run references that run commands accept.
const runArguments = `A run can be supplied as its ID ("34", "#34") or its URL
("https://gitea.example.com/OWNER/REPO/actions/runs/34"). Without an argument,
gt asks for one of the repository's recent runs in a terminal.`

func (a *application) runCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "run",
		Short: "View and manage Actions workflow runs",
		Long:  "Work with the runs of a repository's Actions workflows. Start a run with gt workflow run.",
	}
	group.AddCommand(a.runListCommand(), a.runViewCommand(), a.runWatchCommand(), a.runCancelCommand(), a.runRerunCommand(), a.runDeleteCommand(),
		a.runJobsCommand(), a.runLogsCommand(), a.runDownloadCommand(), a.artifactCommand())
	// Only gt run view and gt run watch fetch the jobs of a run.
	return jsonOutput(group, runFieldsWithoutJobs())
}

// runFieldsWithoutJobs are the JSON fields of a run for the commands that do not fetch its jobs.
func runFieldsWithoutJobs() []string {
	return slices.DeleteFunc(jsonFields[services.Run](), func(field string) bool { return field == "jobs" })
}

func (a *application) runListCommand() *cobra.Command {
	var filters services.RunListOptions
	var created timeRange
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workflow runs",
		Long: `List the workflow runs of a repository, newest first.

--since and --until compare the time a run was created; gt scans up to 5000 runs to apply them.`,
		Example: `# list recent runs
$ gt run list
# list failed runs of the CI workflow on main
$ gt run list --workflow CI --branch main --status failure
# list the runs created in the last day as JSON
$ gt run list --since 24h --json id,status,conclusion,workflow`,
		Args: argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			if filters.Since, filters.Until, err = created.parse(a.deps.Now()); err != nil {
				return err
			}
			if err := filters.Validate(); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			if filters.Commit, err = a.fullCommitSHA(cmd.Context(), filters.Commit); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			a.accountNote(cmd, t, "to list the runs someone triggered, use --actor")
			service := services.Actions{Client: t.client, Repository: t.Repo}
			opts := filters
			opts.Actor = t.expandMe(opts.Actor)
			if opts.Workflow != "" {
				workflow, err := service.FindWorkflow(cmd.Context(), opts.Workflow)
				// The server also lists the runs of a workflow file that only exists on another branch or was removed.
				if notFound, ok := errors.AsType[*services.NotFoundError](err); ok && notFound.Kind == "workflow" && isWorkflowFile(opts.Workflow) {
					err = nil
				} else if err == nil {
					opts.Workflow = workflow.ID
				}
				if err != nil {
					return err
				}
			}
			page, err := service.RunsPage(cmd.Context(), opts)
			if errors.Is(err, services.ErrRunScanExhausted) {
				return fmt.Errorf("%w; narrow the search with --branch, --workflow or --status", err)
			}
			if err != nil {
				return err
			}
			empty := "no runs in " + t.Repo
			if filters.Workflow+filters.Branch+filters.Event+filters.Actor+filters.Commit != "" || len(filters.Statuses) > 0 || filters.Since != nil || filters.Until != nil {
				empty = "no runs match your filters in " + t.Repo
			}
			spec := listSpec[services.Run]{Columns: runColumns, Row: runRow(a.deps.Now()), Empty: empty, Context: "runs in " + t.Repo}
			return writeTable(a, cmd, page.Items, spec, servicePage(page, filters.Page))
		},
	}
	pageFlags(cmd, &filters.Limit, &filters.Page)
	created.register(cmd, "since", "Runs created at or after a date, RFC3339 timestamp, or duration ago (24h)", "until", "Runs created at or before a date, RFC3339 timestamp, or duration ago")
	cmd.Flags().StringVarP(&filters.Workflow, "workflow", "w", "", "Filter by workflow file or name")
	cmd.Flags().StringVarP(&filters.Branch, "branch", "b", "", "Filter by branch")
	cmd.Flags().StringVarP(&filters.Event, "event", "e", "", "Filter by the event that triggered the run, such as push")
	cmd.Flags().StringVarP(&filters.Actor, "actor", "u", "", "Filter by the user who triggered the run (@me for yourself)")
	cmd.Flags().StringVarP(&filters.Commit, "commit", "c", "", "Filter by the commit that triggered the run: its full SHA, or a revision of the local clone")
	cmd.Flags().StringSliceVarP(&filters.Statuses, "status", "s", nil, "Filter by status or conclusion")
	enumValues(cmd, "status", services.RunStatuses...)
	_ = cmd.RegisterFlagCompletionFunc("workflow", a.completeWorkflows())
	_ = cmd.RegisterFlagCompletionFunc("branch", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("event", completeValues(runEvents...))
	_ = cmd.RegisterFlagCompletionFunc("actor", a.completeUsers())
	return cmd
}

// fullCommitSHA returns the full SHA of commit for --commit, which Gitea matches only in full: a
// shorter SHA or another revision is resolved in the local clone.
func (a *application) fullCommitSHA(ctx context.Context, commit string) (string, error) {
	if commit == "" || isFullSHA(strings.ToLower(commit)) {
		return strings.ToLower(commit), nil
	}
	full, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "--quiet", "--end-of-options", commit+"^{commit}")
	if err != nil || !isFullSHA(full) {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("--commit requires a full commit SHA, or a revision of the local clone; %q is neither", commit))
	}
	return full, nil
}

// isFullSHA reports whether s is a full SHA-1 or SHA-256 commit ID in lowercase hex.
func isFullSHA(s string) bool {
	return (len(s) == 40 || len(s) == 64) && strings.Trim(s, "0123456789abcdef") == ""
}

// runEvents are the events that commonly trigger runs, for completion.
var runEvents = []string{"push", "pull_request", "pull_request_target", "workflow_dispatch", "schedule", "release", "create", "delete", "issues", "issue_comment"}

// runColumns head runRow: an icon on a terminal, the status and conclusion words for scripts.
var runColumns = []terminal.Column{
	{Header: "Status", TTYOnly: true},
	{Header: "Status", PipedOnly: true},
	{Header: "Conclusion", PipedOnly: true},
	{Header: "ID"},
	{Header: "Workflow"},
	{Header: "Title", Flex: true},
	{Header: "Branch", Flex: true},
	{Header: "Event"},
	{Header: "Elapsed"},
	{Header: "Age"},
	{Header: "URL", PipedOnly: true},
}

func runRow(now time.Time) func(services.Run) []terminal.Cell {
	return func(run services.Run) []terminal.Cell {
		icon := terminal.StatusIcon(run.Status, run.Conclusion)
		icon.Link = run.URL
		return []terminal.Cell{
			icon,
			{Text: run.Status},
			{Text: run.Conclusion},
			{Text: strconv.FormatInt(run.ID, 10)},
			{Text: run.Workflow},
			{Text: run.Title},
			{Text: run.Branch},
			{Text: run.Event},
			{Text: runElapsedText(run.StartedAt, run.CompletedAt, now)},
			{Time: terminal.ParseTime(run.CreatedAt)},
			{Text: run.URL},
		}
	}
}

// runElapsed is how long a run, job or step has been running: until it completed, or until now. ok
// is false before it starts.
func runElapsed(started, completed *string, now time.Time) (d time.Duration, ok bool) {
	if started == nil {
		return 0, false
	}
	start := terminal.ParseTime(*started)
	if start.IsZero() {
		return 0, false
	}
	end := now
	if completed != nil {
		if t := terminal.ParseTime(*completed); !t.IsZero() {
			end = t
		}
	}
	return max(end.Sub(start), 0), true
}

func runElapsedText(started, completed *string, now time.Time) string {
	if d, ok := runElapsed(started, completed, now); ok {
		return terminal.Duration(d)
	}
	return ""
}

// runStateCell names the state of a run, job or step, its conclusion once it completed, colored
// like its status icon.
func runStateCell(status, conclusion string) terminal.Cell {
	cell := terminal.StatusIcon(status, conclusion)
	cell.Text = cmp.Or(conclusion, status)
	return cell
}

func (a *application) runViewCommand() *cobra.Command {
	var noJobs, web, log bool
	cmd := &cobra.Command{
		Use:   "view [<run-id>]",
		Short: "View a workflow run",
		Long:  "View a workflow run with its jobs and their steps.",
		Example: `# view a run with its jobs and steps
$ gt run view 34
# print the logs of every job of a run
$ gt run view 34 --log
# get the conclusion of each job as JSON
$ gt run view 34 --json jobs --jq '.jobs[] | {name, conclusion}'`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if log && (web || a.options.json != "") {
				return clierr.Wrap(clierr.Usage, errors.New("--log cannot be combined with --web, --json or --jq"))
			}
			t, id, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			if web {
				return a.openBrowser(cmd, fmt.Sprintf("%s/actions/runs/%d", repositoryURL(t), id))
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			if log {
				jobs, err := service.Jobs(cmd.Context(), id)
				if err != nil {
					return err
				}
				return a.printJobLogs(cmd, service, jobs)
			}
			run, err := service.RunDetails(cmd.Context(), id)
			if err != nil {
				return err
			}
			if noJobs {
				run.Jobs = nil
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, run)
			}
			return a.writeHuman(cmd, renderRun(run, a.display(), a.runHints(t, run)))
		},
	}
	cmd.Flags().BoolVar(&noJobs, "no-jobs", false, "Leave out the jobs")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the run in the browser")
	cmd.Flags().BoolVar(&log, "log", false, "Print the logs of every job instead, like gt run logs")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	return jsonOutput(cmd, jsonFields[services.Run]())
}

// isWorkflowFile tells a workflow file name, which the server resolves itself, from a workflow name.
func isWorkflowFile(raw string) bool {
	ext := path.Ext(raw)
	return !strings.ContainsAny(raw, " /") && (ext == ".yml" || ext == ".yaml")
}

// runHints are the next steps shown under a run: watching it while it runs, and reading the logs
// of its jobs once one started.
func (a *application) runHints(t target, run services.Run) []string {
	repo := a.suggestedRepo(t)
	var hints []string
	if !run.Completed() {
		hints = append(hints, fmt.Sprintf("Watch it: gt run watch %d%s", run.ID, repo))
	}
	if slices.ContainsFunc(run.Jobs, services.Job.Started) {
		hints = append(hints, fmt.Sprintf("Logs of a job: gt run logs %d%s --job <id>", run.ID, repo))
	}
	return hints
}

// suggestedRepo repeats --repo for commands that gt suggests, unless the repository came from the
// working directory, where the suggestion finds it again.
func (a *application) suggestedRepo(t target) string {
	if t.repoFromClone() {
		return ""
	}
	// An instance that --host or the repository itself selected is not the one a copied command finds.
	if (t.HostSource == "--host" || (t.RepoSource != "" && t.HostSource == t.RepoSource)) && !a.isDefaultHost(t.Host) {
		return " -R " + t.Host + "/" + t.Repo
	}
	return " -R " + t.Repo
}

// isDefaultHost reports whether host is the instance that commands use without --host or a repository URL.
func (a *application) isDefaultHost(host string) bool {
	s, err := a.settings()
	if err != nil {
		return false
	}
	var fallback target
	return a.defaultHost(&fallback, s) == nil && fallback.Host == host
}

// renderRun shows a run with its jobs: on a terminal its state, trigger and jobs with their
// steps, followed by hints and the run's URL; for scripts "key:\tvalue" lines and the jobs.
func renderRun(run services.Run, d terminal.Display, hints []string) string {
	now := cmp.Or(d.Now, time.Now())
	if !d.TTY {
		return runRecord(run, d, now)
	}
	style := func(cell terminal.Cell) string { return cell.Render(d) }
	muted := func(text string) string { return style(terminal.Cell{Text: text, Muted: true}) }
	var b strings.Builder
	suffix := fmt.Sprintf(" · run %d", run.ID)
	if run.RunNumber > 0 {
		suffix = fmt.Sprintf(" #%d", run.RunNumber) + suffix
	}
	icon := terminal.StatusIcon(run.Status, run.Conclusion)
	title, workflow := terminal.SafeLine(run.Title), terminal.SafeLine(run.Workflow)
	if d.Width > 0 {
		title, workflow = fitHeading(d.Width-runewidth.StringWidth(icon.Text+"  "+suffix), title, workflow)
	}
	b.WriteString(style(icon) + " " + style(terminal.Cell{Text: title, Bold: true}) + " " + muted(workflow+suffix) + "\n")

	facts := []terminal.Cell{runStateCell(run.Status, run.Conclusion)}
	if run.Branch != "" {
		facts = append(facts, terminal.Cell{Text: run.Branch})
	}
	if len(run.Commit) >= 7 {
		facts = append(facts, terminal.Cell{Text: run.Commit[:7]})
	}
	trigger := "triggered"
	if run.Actor != "" {
		trigger = run.Actor + " triggered it"
	}
	if run.Event != "" {
		trigger += " via " + run.Event
	}
	if created := terminal.ParseTime(run.CreatedAt); !created.IsZero() {
		trigger += " " + terminal.RelativeTime(now, created)
	}
	facts = append(facts, terminal.Cell{Text: trigger, Muted: true})
	if d, ok := runElapsed(run.StartedAt, run.CompletedAt, now); ok {
		if run.Completed() {
			facts = append(facts, terminal.Cell{Text: "took " + terminal.Duration(d), Muted: true})
		} else {
			facts = append(facts, terminal.Cell{Text: "running for " + terminal.Duration(d), Muted: true})
		}
	}
	if run.Attempt > 1 {
		facts = append(facts, terminal.Cell{Text: fmt.Sprintf("attempt %d", run.Attempt), Muted: true})
	}
	b.WriteString(joinFacts(facts, d))

	if len(run.Jobs) > 0 {
		b.WriteString("\n" + style(terminal.Cell{Text: "JOBS", Bold: true}) + "\n")
		b.WriteString(terminal.Table(jobLineColumns, jobLines(run.Jobs, now), d))
	}
	if waiting := waitingForRunner(run); waiting != "" {
		b.WriteString("\n" + muted(waiting) + "\n")
	}
	if len(hints) > 0 || run.URL != "" {
		b.WriteByte('\n')
	}
	for _, hint := range hints {
		b.WriteString(muted(hint) + "\n")
	}
	if run.URL != "" {
		b.WriteString(muted("View this run on Gitea: "+terminal.SafeLine(run.URL)) + "\n")
	}
	return b.String()
}

// fitHeading cuts the run's title, then its workflow, with "…" until both fit in room cells; the
// title keeps at least eight cells.
func fitHeading(room int, title, workflow string) (string, string) {
	over := runewidth.StringWidth(title) + runewidth.StringWidth(workflow) - room
	if width := runewidth.StringWidth(title); over > 0 && width > 8 {
		cut := min(over, width-8)
		title = runewidth.Truncate(title, width-cut, "…")
		over -= cut
	}
	if width := runewidth.StringWidth(workflow); over > 0 && width > 0 {
		workflow = runewidth.Truncate(workflow, max(width-over, 1), "…")
	}
	return title, workflow
}

// joinFacts joins the facts of the run view with " • ", starting a new line instead before a fact
// that would pass the display width, and cutting a fact wider than the whole line with "…".
func joinFacts(facts []terminal.Cell, d terminal.Display) string {
	const separator = " • "
	var b strings.Builder
	used := 0
	for i, fact := range facts {
		fact.Text = terminal.SafeLine(fact.Text)
		if d.Width > 0 {
			fact.Text = runewidth.Truncate(fact.Text, d.Width, "…")
		}
		width := runewidth.StringWidth(fact.Text)
		switch {
		case i == 0:
		case d.Width > 0 && used+runewidth.StringWidth(separator)+width > d.Width:
			b.WriteByte('\n')
			used = 0
		default:
			b.WriteString(separator)
			used += runewidth.StringWidth(separator)
		}
		b.WriteString(fact.Render(d))
		used += width
	}
	return b.String() + "\n"
}

// jobLineColumns fit the job and step names of the run view to the terminal: Gitea names a run: step
// after its whole script, "Run <script>".
var jobLineColumns = []terminal.Column{{}, {Flex: true}}

// jobLines lists jobs, each followed by its steps, for the run view.
func jobLines(jobs []services.Job, now time.Time) [][]terminal.Cell {
	var rows [][]terminal.Cell
	for _, job := range jobs {
		icon := terminal.StatusIcon(job.Status, job.Conclusion)
		icon.Link = job.URL
		rows = append(rows, []terminal.Cell{icon, {Text: job.Name, Bold: true}, runStateCell(job.Status, job.Conclusion), {Text: runElapsedText(job.StartedAt, job.CompletedAt, now)}, {Text: fmt.Sprintf("(ID %d)", job.ID), Muted: true}})
		for _, step := range job.Steps {
			stepIcon := terminal.StatusIcon(step.Status, step.Conclusion)
			row := []terminal.Cell{{}, {Text: "  " + stepIcon.Text + " " + step.Name, Muted: true}, {}, {Text: runElapsedText(step.StartedAt, step.CompletedAt, now), Muted: true}}
			if step.Conclusion == "failure" {
				row[1].Muted, row[1].Color = false, "red"
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// waitingForRunner explains a run that waits for a runner: none of its jobs started yet. A run
// that needs approval, such as one from a first-time contributor's fork, has the status "waiting".
func waitingForRunner(run services.Run) string {
	if run.Completed() || len(run.Jobs) == 0 || slices.ContainsFunc(run.Jobs, services.Job.Started) {
		return ""
	}
	if run.Status == "waiting" {
		return "Waiting for approval; approve the run in the web interface"
	}
	return "Waiting for a runner" + runnerLabels(run)
}

// runnerLabels names the runner labels that the run's jobs need: ` for "ubuntu-latest"`.
func runnerLabels(run services.Run) string {
	var labels []string
	for _, job := range run.Jobs {
		for _, label := range job.Labels {
			if label = strconv.Quote(terminal.SafeLine(label)); !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return " for " + strings.Join(labels, ", ")
}

// runRecord shows a run for scripts: "key:\tvalue" lines, then the jobs tab-separated.
func runRecord(run services.Run, d terminal.Display, now time.Time) string {
	optional := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	fields := []terminal.Field{
		{Label: "ID", Value: strconv.FormatInt(run.ID, 10)},
		{Label: "Workflow", Value: run.Workflow},
		{Label: "Workflow_path", Value: run.WorkflowPath},
		{Label: "Run_number", Value: strconv.FormatInt(run.RunNumber, 10)},
		{Label: "Status", Value: run.Status},
		{Label: "Conclusion", Value: run.Conclusion},
		{Label: "Branch", Value: run.Branch},
		{Label: "Commit", Value: run.Commit},
		{Label: "Event", Value: run.Event},
		{Label: "Actor", Value: run.Actor},
		{Label: "Attempt", Value: strconv.FormatInt(run.Attempt, 10)},
		{Label: "Created_at", Value: pipedTime(run.CreatedAt)},
		{Label: "Started_at", Value: pipedTime(optional(run.StartedAt))},
		{Label: "Completed_at", Value: pipedTime(optional(run.CompletedAt))},
		{Label: "Elapsed", Value: runElapsedText(run.StartedAt, run.CompletedAt, now)},
	}
	rows := make([][]terminal.Cell, len(run.Jobs))
	for i, job := range run.Jobs {
		rows[i] = jobRow(now)(job)
	}
	return terminal.View{Title: run.Title, Fields: fields, URL: run.URL, Body: terminal.Table(jobColumns, rows, d)}.Render(d)
}

// runArg resolves the run that a command addresses: args[0], a run ID or URL, or else, in a
// terminal, one of the repository's recent runs that the user picks; active offers only runs
// that have not completed.
func (a *application) runArg(cmd *cobra.Command, args []string, active bool) (target, int64, error) {
	if len(args) > 0 {
		return a.resolveRef(cmd, args[0], runRef)
	}
	missing := clierr.Wrap(clierr.Usage, &argumentError{problem: "missing run ID", cmd: cmd})
	chooser, ok := a.chooser()
	if !ok {
		return target{}, 0, missing
	}
	t, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return t, 0, err
	}
	page, err := services.Actions{Client: t.client, Repository: t.Repo}.RunsPage(cmd.Context(), services.RunListOptions{Limit: 30, Page: 1})
	if err != nil {
		return t, 0, err
	}
	runs := page.Items
	if active {
		runs = slices.DeleteFunc(runs, services.Run.Completed)
	}
	if len(runs) == 0 {
		if active {
			return t, 0, fmt.Errorf("no runs in progress in %s; pass a run ID", t.Repo)
		}
		return t, 0, fmt.Errorf("no runs in %s", t.Repo)
	}
	now := a.deps.Now()
	choices := make([]terminal.Choice, 0, 10)
	for _, run := range runs[:min(len(runs), 10)] {
		label := fmt.Sprintf("%s %s · %s · %s (run %d, %s)", terminal.StatusIcon(run.Status, run.Conclusion).Text, run.Title, run.Workflow, run.Branch, run.ID, terminal.RelativeTime(now, terminal.ParseTime(run.CreatedAt)))
		choices = append(choices, terminal.Choice{Label: label, Value: strconv.FormatInt(run.ID, 10)})
	}
	chosen, err := chooser.Choose(cmd.Context(), "Select a run", choices, "")
	if err != nil {
		return t, 0, err
	}
	id, err := strconv.ParseInt(chosen, 10, 64)
	return t, id, err
}

// accountNote reminds people who pass the global --user, which selects the account gt acts as,
// of the flag they may have meant, as gh's --user means something else.
func (a *application) accountNote(cmd *cobra.Command, t target, hint string) {
	if flag := cmd.Flag("user"); flag != nil && flag.Changed && a.deps.Streams.ErrTTY {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: --user selects the account gt acts as (%s); %s\n", terminal.SafeLine(t.User), hint)
	}
}

func (a *application) runWatchCommand() *cobra.Command {
	var interval, timeout time.Duration
	cmd := &cobra.Command{
		Use:   "watch [<run-id>]",
		Short: "Watch a run until it completes",
		Long: `Watch a run until it completes, refreshing its jobs and steps on a terminal.

The command fails when the run completes without success. It polls on through an outage of the
instance of up to 5 minutes.`,
		Example: `# watch a run
$ gt run watch 34
# pick one of the runs in progress
$ gt run watch
# wait for a run in a script, for at most 10 minutes
$ gt run watch 34 --timeout 10m && echo deployed`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if interval <= 0 || timeout <= 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
			}
			t, id, err := a.runArg(cmd, args, true)
			if err != nil {
				return err
			}
			return a.watchRun(cmd, t, id, interval, timeout)
		},
	}
	cmd.Flags().DurationVarP(&interval, "interval", "i", 3*time.Second, "Refresh interval")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Hour, "Maximum time to wait")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	return jsonOutput(cmd, jsonFields[services.Run]())
}

// watchRun polls run id until it completes, redrawing the run view on the alternate screen of a
// terminal, or else reporting each change of state on stderr.
func (a *application) watchRun(cmd *cobra.Command, t target, id int64, interval, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	out := cmd.OutOrStdout()
	live := a.liveRedraw()
	if live {
		fmt.Fprint(out, "\x1b[?1049h")
	}
	leave := func() {
		if live {
			fmt.Fprint(out, "\x1b[?1049l")
			live = false
		}
	}
	defer leave()
	last := ""
	service := services.Actions{Client: t.client, Repository: t.Repo}
	run, err := service.Watch(ctx, id, interval, func(run services.Run) error {
		if live {
			d := a.display()
			_, err := fmt.Fprintf(out, "\x1b[H\x1b[J%s\n\n%s", terminal.Colorize("muted", fmt.Sprintf("Refreshing run status every %s. Press Ctrl+C to quit.", shortDuration(interval)), d.Color), renderRun(run, d, nil))
			return err
		}
		if state := runProgress(run); state != last {
			last = state
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), state)
			return err
		}
		return nil
	})
	leave()
	if err != nil {
		if expired(err) && run.ID != 0 {
			if a.options.json == "" {
				// Not through the pager, which would stop the command at its end.
				_, _ = io.WriteString(out, renderRun(run, a.display(), nil))
			}
			return timeoutError(err, timeout, "waiting for run %d (%s)", id, stillRunning(run))
		}
		return timeoutError(err, timeout, "waiting for run %d", id)
	}
	if a.options.json != "" {
		if err := a.writeJSON(cmd, run); err != nil {
			return err
		}
	} else if _, err := io.WriteString(out, renderRun(run, a.display(), nil)); err != nil {
		return err
	}
	if err := runFailure(run); err != nil {
		return err
	}
	conclusion := terminal.SafeLine(cmp.Or(run.Conclusion, run.Status))
	took := ""
	if d, ok := runElapsed(run.StartedAt, run.CompletedAt, a.deps.Now()); ok {
		took = " in " + terminal.Duration(d)
	}
	a.done(cmd, "Run %d (%s) completed with %s%s", run.ID, run.Workflow, conclusion, took)
	return nil
}

// runFailure fails for a completed run that did not succeed.
func runFailure(run services.Run) error {
	if services.SuccessfulRun(run) {
		return nil
	}
	return fmt.Errorf("run %d (%s) completed with %s", run.ID, terminal.SafeLine(run.Workflow), terminal.SafeLine(cmp.Or(run.Conclusion, run.Status)))
}

// runProgress is one line about the state of a run and its jobs, for watching without a
// terminal: "run 34: in_progress (build: success, test: in_progress)".
func runProgress(run services.Run) string {
	jobs := make([]string, len(run.Jobs))
	for i, job := range run.Jobs {
		jobs[i] = terminal.SafeLine(job.Name) + ": " + cmp.Or(job.Conclusion, job.Status)
	}
	line := fmt.Sprintf("run %d: %s", run.ID, cmp.Or(run.Conclusion, run.Status))
	if len(jobs) > 0 {
		line += " (" + strings.Join(jobs, ", ") + ")"
	}
	return line
}

// stillRunning says how far a run got when a wait for it expired, suspecting a missing runner
// when no job started.
func stillRunning(run services.Run) string {
	if run.Status == "waiting" && waitingForRunner(run) != "" {
		return "still waiting for approval in the web interface"
	}
	if waitingForRunner(run) != "" {
		return "still " + terminal.SafeLine(run.Status) + "; is a runner registered" + runnerLabels(run) + "?"
	}
	return "still " + terminal.SafeLine(run.Status)
}

func (a *application) runCancelCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "cancel [<run-id>]",
		Short: "Cancel a workflow run",
		Long:  "Cancel a workflow run. The run and its logs are kept; rerun it with gt run rerun.",
		Example: `# cancel a run
$ gt run cancel 34
# cancel a run whose runner no longer responds
$ gt run cancel 34 --force`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			t, id, err := a.runArg(cmd, args, true)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			run, err := service.CancelRun(cmd.Context(), id, force)
			if err != nil {
				return err
			}
			a.done(cmd, "Requested cancellation of run %d", id)
			if a.options.json != "" {
				run = service.NameRun(cmd.Context(), run)
			}
			return a.jsonResult(cmd, run)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Cancel without waiting for the runner to acknowledge")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	return cmd
}

func (a *application) runRerunCommand() *cobra.Command {
	var failed bool
	var job string
	cmd := &cobra.Command{
		Use:   "rerun [<run-id>]",
		Short: "Rerun a workflow run",
		Long:  "Rerun all jobs of a completed workflow run, only its failed jobs, or a single job, which --job or\nthe URL of the job selects.",
		Example: `# rerun a run
$ gt run rerun 34
# rerun only the jobs that failed
$ gt run rerun 34 --failed
# rerun one job, by name or ID
$ gt run rerun 34 --job build`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			if failed && job != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--failed cannot be combined with --job"))
			}
			conflicting := ""
			if failed {
				conflicting = "--failed"
			}
			selected, urlJob, err := jobOfRunURL(args, job, conflicting)
			if err != nil {
				return err
			}
			job = selected
			t, id, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			run, err := service.Run(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !run.Completed() {
				return a.unfinished(t, run)
			}
			var jobID int64
			if job != "" {
				jobs, err := service.Jobs(cmd.Context(), id)
				if err != nil {
					return err
				}
				selected, err := findJob(jobs, job, id)
				if err != nil {
					return err
				}
				if err := sameJob(job, selected, urlJob); err != nil {
					return err
				}
				jobID = selected.ID
			}
			if err := service.Rerun(cmd.Context(), id, jobID, failed); err != nil {
				return err
			}
			if run, err = service.Run(cmd.Context(), id); err != nil {
				return err
			}
			a.done(cmd, "Requested rerun of run %d (attempt %d); watch it with: gt run watch %d%s", id, run.Attempt, id, a.suggestedRepo(t))
			if a.options.json != "" {
				run = service.NameRun(cmd.Context(), run)
			}
			return a.jsonResult(cmd, run)
		},
	}
	cmd.Flags().BoolVar(&failed, "failed", false, "Rerun only the failed jobs")
	cmd.Flags().StringVarP(&job, "job", "j", "", "Rerun only this job, by `name or ID`")
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	_ = cmd.RegisterFlagCompletionFunc("job", a.completeJobs())
	return cmd
}

// unfinished refuses to act on a run that has not completed, as Gitea does, before asking anything.
func (a *application) unfinished(t target, run services.Run) error {
	return fmt.Errorf("run %d is still %s; cancel it first: gt run cancel %d%s", run.ID, run.Status, run.ID, a.suggestedRepo(t))
}

// jobOfRunURL returns the --job value job, or the ID of the job that a job URL in args selects,
// such as https://HOST/OWNER/REPO/actions/runs/N/jobs/M, with the ID of that URL's job or 0.
// conflicting names a flag passed that cannot be combined with selecting one job, or is empty.
// A --job name is checked against the URL's job by sameJob once the jobs are known.
func jobOfRunURL(args []string, job, conflicting string) (string, int64, error) {
	if len(args) == 0 {
		return job, 0, nil
	}
	parsed, err := parseRef(args[0], runRef)
	if err != nil || parsed.Job == 0 {
		return job, 0, nil
	}
	if conflicting != "" {
		return "", 0, clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with the URL of job %d", conflicting, parsed.Job))
	}
	return cmp.Or(job, strconv.FormatInt(parsed.Job, 10)), parsed.Job, nil
}

// sameJob refuses a --job value that selected another job than the job URL, whose job ID is
// urlJob, or 0 without a job URL.
func sameJob(raw string, selected services.Job, urlJob int64) error {
	if urlJob == 0 || selected.ID == urlJob {
		return nil
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("--job %s cannot be combined with the URL of job %d", raw, urlJob))
}

func findJob(jobs []services.Job, raw string, run int64) (services.Job, error) {
	return services.NameOrID[services.Job]{
		Kind: "job", Scope: fmt.Sprintf("run %d", run),
		ID: func(job services.Job) int64 { return job.ID }, Name: func(job services.Job) string { return job.Name },
	}.Find(jobs, raw)
}

func (a *application) runDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [<run-id>]",
		Short: "Delete a workflow run",
		Long:  "Delete a completed workflow run with its logs and artifacts. Cancel a run before deleting it.",
		Example: `# delete a run
$ gt run delete 34
# delete a run without confirmation
$ gt run delete 34 --yes`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments, "gt.confirm": "true", "gt.text": "run deletion does not return a resource"},
		RunE: func(cmd *cobra.Command, args []string) error {
			t, id, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			service := services.Actions{Client: t.client, Repository: t.Repo}
			run, err := service.RunDetails(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !run.Completed() {
				return a.unfinished(t, run)
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{
				Prompt: fmt.Sprintf("Delete run %d (%s · %s) from %s?", id, run.Workflow, run.Title, t.Repo),
				NonTTY: fmt.Sprintf("delete run %d from %s", id, t.Repo),
			}); err != nil {
				return err
			}
			if err := service.DeleteRun(cmd.Context(), id); err != nil {
				return err
			}
			a.done(cmd, "Deleted run %d from %s", id, t.Repo)
			return nil
		},
	}
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	return cmd
}

// completeJobs completes the jobs of the run given as the first argument with their names.
func (a *application) completeJobs() cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		run, err := parseRef(args[0], runRef)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return a.apiCompletionIn(run.Repo, true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			jobs, err := services.Actions{Client: t.client, Repository: t.Repo}.Jobs(ctx, run.Number)
			return describe(jobs, func(job services.Job) (string, string) {
				return strconv.FormatInt(job.ID, 10), job.Name + " · " + cmp.Or(job.Conclusion, job.Status)
			}), err
		})(cmd, args, toComplete)
	}
}
