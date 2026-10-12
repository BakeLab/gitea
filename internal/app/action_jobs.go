// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"fmt"
	"strconv"
	"time"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) runJobsCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{
		Use:   "jobs [<run-id>]",
		Short: "List the jobs of a workflow run",
		Example: `# list the jobs of a run
$ gt run jobs 34
# list the IDs of the jobs that failed
$ gt run jobs 34 --json id,conclusion --jq '.[] | select(.conclusion == "failure") | .id'`,
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		Annotations: map[string]string{"gt.arguments": runArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			t, run, err := a.runArg(cmd, args, false)
			if err != nil {
				return err
			}
			jobs, err := (services.Actions{Client: t.client, Repository: t.Repo}).JobsPage(cmd.Context(), run, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.Job]{Columns: jobColumns, Row: jobRow(a.deps.Now()), Empty: fmt.Sprintf("no jobs in run %d", run), Context: fmt.Sprintf("jobs in run %d", run)}
			return writeTable(a, cmd, jobs.Items, spec, servicePage(jobs, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	cmd.ValidArgsFunction = firstArg(a.completeRuns())
	return cmd
}

// jobColumns head jobRow: an icon on a terminal, the status and conclusion words for scripts.
var jobColumns = []terminal.Column{
	{Header: "Status", TTYOnly: true},
	{Header: "Status", PipedOnly: true},
	{Header: "Conclusion", PipedOnly: true},
	{Header: "ID"},
	{Header: "Name", Flex: true},
	{Header: "Elapsed"},
	{Header: "URL", PipedOnly: true},
}

func jobRow(now time.Time) func(services.Job) []terminal.Cell {
	return func(job services.Job) []terminal.Cell {
		icon := terminal.StatusIcon(job.Status, job.Conclusion)
		icon.Link = job.URL
		return []terminal.Cell{
			icon,
			{Text: job.Status},
			{Text: job.Conclusion},
			{Text: strconv.FormatInt(job.ID, 10)},
			{Text: job.Name},
			{Text: runElapsedText(job.StartedAt, job.CompletedAt, now)},
			{Text: job.URL},
		}
	}
}
