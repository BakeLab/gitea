// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) workflowCommand() *cobra.Command {
	group := &cobra.Command{Use: "workflow", Short: "Manage Actions workflows"}
	group.AddCommand(&cobra.Command{Use: "list", Short: "List repository workflows", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Actions{Client: t.client, Repository: t.Repo}).Workflows(cmd.Context())
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, workflowRow)
	}})
	group.AddCommand(&cobra.Command{Use: "view <id>", Short: "View a workflow", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Actions{Client: t.client, Repository: t.Repo}).Workflow(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, workflowRow)
	}})
	var ref string
	var fields, inputs []string
	var follow bool
	dispatch := &cobra.Command{Use: "run <id>", Short: "Dispatch a workflow", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if follow && a.options.json != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--follow cannot be combined with --json/--jq"))
		}
		if ref == "" {
			branch, err := a.currentBranch(cmd.Context())
			if err != nil || branch == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--ref is required outside a branch checkout"))
			}
			ref = branch
		}
		values := map[string]string{}
		for _, field := range append(fields, inputs...) {
			pieces := strings.SplitN(field, "=", 2)
			if len(pieces) != 2 || pieces[0] == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--field/--input requires key=value"))
			}
			if _, exists := values[pieces[0]]; exists {
				return clierr.Wrap(clierr.Usage, errors.New("duplicate workflow input"))
			}
			values[pieces[0]] = pieces[1]
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Actions{Client: t.client, Repository: t.Repo}
		result, err := service.Dispatch(cmd.Context(), args[0], ref, values)
		if err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		if result.ID == 0 {
			if follow {
				return errors.New("server did not return a run ID; cannot follow logs")
			}
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Workflow dispatched; the server did not return a run ID")
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\n", result.ID, result.URL); err != nil {
			return err
		}
		if !follow {
			return nil
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Waiting for run %d jobs...\n", result.ID)
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Minute)
		defer cancel()
		job, err := service.WaitForFirstJob(ctx, result.ID, 2*time.Second)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Following job %d (%s)\n", job.ID, job.Name)
		return service.FollowJobLogs(ctx, result.ID, job.ID, 2*time.Second, func(chunk []byte) error {
			_, err := cmd.OutOrStdout().Write(chunk)
			return err
		})
	}}
	dispatch.Flags().StringVarP(&ref, "ref", "r", "", "Branch or tag to run (default: current branch)")
	dispatch.Flags().StringArrayVarP(&fields, "field", "f", nil, "Workflow input key=value (repeatable)")
	dispatch.Flags().StringArrayVarP(&inputs, "input", "i", nil, "Alias for --field")
	dispatch.Flags().BoolVar(&follow, "follow", false, "Follow the first job's logs after dispatch")
	group.AddCommand(dispatch)
	for _, verb := range []string{"enable", "disable"} {
		group.AddCommand(&cobra.Command{Use: verb + " <id>", Short: titleCase(verb) + " a workflow", Annotations: textOutput("workflow state changes do not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if verb == "disable" {
				if err := a.confirm(cmd, "Disable workflow "+args[0]); err != nil {
					return err
				}
			}
			return (services.Actions{Client: t.client, Repository: t.Repo}).SetWorkflowEnabled(cmd.Context(), args[0], verb == "enable")
		}})
	}
	return group
}

func workflowRow(item services.Workflow) []terminal.Cell {
	return terminal.Row(item.ID, item.Name, item.State)
}

func runRow(item services.Run) []terminal.Cell {
	return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10), Link: item.URL}, {Text: item.Title}, {Text: item.Status}, {Text: item.Conclusion}, {Text: item.Branch}, {Text: item.Event}}
}

func (a *application) runCommand() *cobra.Command {
	group := &cobra.Command{Use: "run", Short: "Inspect and watch Actions runs"}
	group.AddCommand(a.runLogsCommand(), a.artifactCommand(), a.runJobsCommand())
	var filters services.RunListOptions
	var started timeRange
	list := &cobra.Command{Use: "list", Short: "List workflow runs", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		var err error
		if filters.Since, filters.Until, err = started.parse(a.deps.Now()); err != nil {
			return err
		}
		if err := filters.Validate(); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Actions{Client: t.client, Repository: t.Repo}).Runs(cmd.Context(), filters)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, runRow)
	}}
	pageFlags(list, &filters.Limit, &filters.Page)
	started.register(list, "since", "Runs started at or after a date, RFC3339 timestamp, or duration ago (24h)", "until", "Runs started at or before a date, RFC3339 timestamp, or duration ago")
	list.Flags().StringVar(&filters.Branch, "branch", "", "Filter by branch name")
	list.Flags().StringVar(&filters.Event, "event", "", "Filter by triggering event")
	list.Flags().StringVar(&filters.Actor, "actor", "", "Filter by triggering username")
	list.Flags().StringVar(&filters.Commit, "commit", "", "Filter by triggering commit SHA")
	list.Flags().StringVar(&filters.Workflow, "workflow", "", "Filter by workflow file name")
	list.Flags().StringSliceVar(&filters.Statuses, "status", nil, "Filter status: pending, waiting, requested, action_required, queued, in_progress, completed, success, failure, skipped, neutral, cancelled, timed_out") //nolint:misspell // Gitea API spelling.
	group.AddCommand(list)
	var includeJobs bool
	viewRun := &cobra.Command{Use: "view <id>", Short: "View a workflow run", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Actions{Client: t.client, Repository: t.Repo}
		item, err := service.Run(cmd.Context(), id)
		if err != nil {
			return err
		}
		if !includeJobs || a.options.json != "" {
			return writeItem(a, cmd, item, runRow)
		}
		jobs, err := service.Jobs(cmd.Context(), id)
		if err != nil {
			return err
		}
		jobLine := func(job services.Job) []terminal.Cell { return append(terminal.Row("job"), jobRow(job)...) }
		color := a.colorEnabled()
		return a.writeHuman(cmd, table([]services.Run{item}, runRow, color)+table(jobs, jobLine, color))
	}}
	viewRun.Flags().BoolVar(&includeJobs, "jobs", true, "Include job summaries after the run details")
	group.AddCommand(viewRun)
	var interval, timeout time.Duration
	watch := &cobra.Command{Use: "watch <id>", Short: "Wait for completion; fail if the run fails", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if interval <= 0 || timeout <= 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--interval and --timeout must be positive"))
		}
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		result, err := (services.Actions{Client: t.client, Repository: t.Repo}).Watch(ctx, id, interval, func(item services.Run) error {
			if a.options.json != "" {
				_, err := fmt.Fprintf(cmd.ErrOrStderr(), "run %d: %s %s\n", item.ID, item.Status, item.Conclusion)
				return err
			}
			return writeItem(a, cmd, item, runRow)
		})
		if err != nil {
			return err
		}
		if a.options.json != "" {
			if err := writeItem(a, cmd, result, runRow); err != nil {
				return err
			}
		}
		if !services.SuccessfulRun(result) {
			return errors.New("workflow run did not succeed")
		}
		return nil
	}}
	watch.Flags().DurationVar(&interval, "interval", 5*time.Second, "Polling interval")
	watch.Flags().DurationVar(&timeout, "timeout", time.Hour, "Maximum time to wait")
	group.AddCommand(watch)
	var forceCancel bool
	cancelRun := &cobra.Command{Use: "cancel <id>", Short: "Cancel a workflow run while preserving its history", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("Cancel run %d", id)
		if forceCancel {
			message = fmt.Sprintf("Force-cancel run %d without waiting for runners", id)
		}
		if err := a.confirm(cmd, message); err != nil {
			return err
		}
		item, err := (services.Actions{Client: target.client, Repository: target.Repo}).CancelRun(cmd.Context(), id, forceCancel)
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, runRow)
	}}
	cancelRun.Flags().BoolVar(&forceCancel, "force", false, "Cancel without waiting for runner acknowledgment")
	group.AddCommand(cancelRun)
	var failedOnly bool
	rerun := &cobra.Command{Use: "rerun <id>", Short: "Rerun a workflow run", Annotations: textOutput("rerun does not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		return (services.Actions{Client: t.client, Repository: t.Repo}).Rerun(cmd.Context(), id, failedOnly)
	}}
	rerun.Flags().BoolVar(&failedOnly, "failed", false, "Rerun failed jobs only")
	group.AddCommand(rerun)
	group.AddCommand(&cobra.Command{Use: "delete <id>", Short: "Delete a workflow run", Annotations: textOutput("run deletion does not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if err := a.confirm(cmd, fmt.Sprintf("Delete run %d", id)); err != nil {
			return err
		}
		return (services.Actions{Client: t.client, Repository: t.Repo}).DeleteRun(cmd.Context(), id)
	}})
	return group
}
