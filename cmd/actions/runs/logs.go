// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runs

import (
	stdctx "context"
	"fmt"
	"strconv"
	"time"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdRunsLogs represents a sub command to view workflow run logs
var CmdRunsLogs = cli.Command{
	Name:        "logs",
	Aliases:     []string{"log"},
	Usage:       "View workflow run logs",
	Description: "View logs for a workflow run or specific job",
	ArgsUsage:   "<run-id>",
	Action:      runRunsLogs,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "job",
			Usage: "specific job ID to view logs for (if omitted, shows all jobs)",
		},
		&cli.BoolFlag{
			Name:    "follow",
			Aliases: []string{"f"},
			Usage:   "follow log output (like tail -f), requires job to be in progress",
		},
	}, flags.AllDefaultFlags...),
}

func runRunsLogs(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("run ID is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	runIDStr := cmd.Args().First()
	runID, err := strconv.ParseInt(runIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid run ID: %s", runIDStr)
	}

	// Check if follow mode is enabled
	follow := cmd.Bool("follow")

	// If specific job ID provided, fetch only that job's logs
	jobIDStr := cmd.String("job")
	if jobIDStr != "" {
		jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid job ID: %s", jobIDStr)
		}

		if follow {
			return followJobLogs(client, c, jobID, "")
		}

		logs, _, err := client.GetRepoActionJobLogs(c.Owner, c.Repo, jobID)
		if err != nil {
			return fmt.Errorf("failed to get logs for job %d: %w", jobID, err)
		}

		fmt.Printf("Logs for job %d:\n", jobID)
		fmt.Printf("---\n%s\n", string(logs))
		return nil
	}

	// Otherwise, fetch all jobs and their logs
	jobs, _, err := client.ListRepoActionRunJobs(c.Owner, c.Repo, runID, gitea.ListRepoActionJobsOptions{
		ListOptions: flags.GetListOptions(),
	})
	if err != nil {
		return fmt.Errorf("failed to get jobs: %w", err)
	}

	if len(jobs.Jobs) == 0 {
		fmt.Printf("No jobs found for run %d\n", runID)
		return nil
	}

	// If following and multiple jobs, require --job flag
	if follow && len(jobs.Jobs) > 1 {
		return fmt.Errorf("--follow requires --job when run has multiple jobs (found %d jobs)", len(jobs.Jobs))
	}

	// If following with single job, follow it
	if follow && len(jobs.Jobs) == 1 {
		return followJobLogs(client, c, jobs.Jobs[0].ID, jobs.Jobs[0].Name)
	}

	// Fetch logs for each job
	for i, job := range jobs.Jobs {
		if i > 0 {
			fmt.Println()
		}

		fmt.Printf("Job: %s (ID: %d)\n", job.Name, job.ID)
		fmt.Printf("Status: %s\n", job.Status)
		fmt.Println("---")

		logs, _, err := client.GetRepoActionJobLogs(c.Owner, c.Repo, job.ID)
		if err != nil {
			fmt.Printf("Error fetching logs: %v\n", err)
			continue
		}

		fmt.Println(string(logs))
	}

	return nil
}

// followJobLogs continuously fetches and displays logs for a running job
func followJobLogs(client *gitea.Client, c *context.TeaContext, jobID int64, jobName string) error {
	var lastLogLength int

	if jobName != "" {
		fmt.Printf("Following logs for job '%s' (ID: %d) - press Ctrl+C to stop...\n", jobName, jobID)
	} else {
		fmt.Printf("Following logs for job %d (press Ctrl+C to stop)...\n", jobID)
	}
	fmt.Println("---")

	for {
		// Fetch job status
		job, _, err := client.GetRepoActionJob(c.Owner, c.Repo, jobID)
		if err != nil {
			return fmt.Errorf("failed to get job: %w", err)
		}

		// Check if job is still running
		isRunning := job.Status == "in_progress" || job.Status == "queued" || job.Status == "pending"

		// Fetch logs
		logs, _, err := client.GetRepoActionJobLogs(c.Owner, c.Repo, jobID)
		if err != nil {
			return fmt.Errorf("failed to get logs: %w", err)
		}

		// Display new content only
		if len(logs) > lastLogLength {
			newLogs := string(logs)[lastLogLength:]
			fmt.Print(newLogs)
			lastLogLength = len(logs)
		}

		// If job is complete, exit
		if !isRunning {
			fmt.Printf("\n---\nJob completed with status: %s\n", job.Status)
			break
		}

		// Wait before next poll
		time.Sleep(2 * time.Second)
	}

	return nil
}
