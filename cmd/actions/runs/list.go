// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runs

import (
	stdctx "context"
	"fmt"
	"time"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdRunsList represents a sub command to list workflow runs
var CmdRunsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List workflow runs",
	Description: "List workflow runs for repository actions with optional filtering",
	Action:      RunRunsList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
		&cli.StringFlag{
			Name:  "status",
			Usage: "Filter by status (success, failure, pending, queued, in_progress, skipped, canceled)",
		},
		&cli.StringFlag{
			Name:  "branch",
			Usage: "Filter by branch name",
		},
		&cli.StringFlag{
			Name:  "event",
			Usage: "Filter by event type (push, pull_request, etc.)",
		},
		&cli.StringFlag{
			Name:  "actor",
			Usage: "Filter by actor username (who triggered the run)",
		},
		&cli.StringFlag{
			Name:  "since",
			Usage: "Show runs started after this time (e.g., '24h', '2024-01-01')",
		},
		&cli.StringFlag{
			Name:  "until",
			Usage: "Show runs started before this time (e.g., '2024-01-01')",
		},
	}, flags.AllDefaultFlags...),
}

// parseTimeFlag parses time flags like "24h" or "2024-01-01"
func parseTimeFlag(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	// Try parsing as duration (e.g., "24h", "168h")
	if duration, err := time.ParseDuration(value); err == nil {
		return time.Now().Add(-duration), nil
	}

	// Try parsing as date
	formats := []string{
		"2006-01-02",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}

	for _, format := range formats {
		if t, err := time.Parse(format, value); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse time: %s", value)
}

// RunRunsList lists workflow runs
func RunRunsList(ctx stdctx.Context, cmd *cli.Command) error {
	c := context.InitCommand(cmd)
	client := c.Login.Client()

	// Parse time filters
	since, err := parseTimeFlag(cmd.String("since"))
	if err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
	}

	until, err := parseTimeFlag(cmd.String("until"))
	if err != nil {
		return fmt.Errorf("invalid --until value: %w", err)
	}

	// Build list options
	listOpts := flags.GetListOptions()

	runs, _, err := client.ListRepoActionRuns(c.Owner, c.Repo, gitea.ListRepoActionRunsOptions{
		ListOptions: listOpts,
		Status:      cmd.String("status"),
		Branch:      cmd.String("branch"),
		Event:       cmd.String("event"),
		Actor:       cmd.String("actor"),
	})
	if err != nil {
		return err
	}

	if runs == nil {
		print.ActionRunsList(nil, c.Output)
		return nil
	}

	// Filter by time if specified
	filteredRuns := filterRunsByTime(runs.WorkflowRuns, since, until)

	print.ActionRunsList(filteredRuns, c.Output)
	return nil
}

// filterRunsByTime filters runs based on time range
func filterRunsByTime(runs []*gitea.ActionWorkflowRun, since, until time.Time) []*gitea.ActionWorkflowRun {
	if since.IsZero() && until.IsZero() {
		return runs
	}

	var filtered []*gitea.ActionWorkflowRun
	for _, run := range runs {
		if !since.IsZero() && run.StartedAt.Before(since) {
			continue
		}
		if !until.IsZero() && run.StartedAt.After(until) {
			continue
		}
		filtered = append(filtered, run)
	}

	return filtered
}
