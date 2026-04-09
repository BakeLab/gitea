// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package workflows

import (
	stdctx "context"
	"fmt"
	"strings"
	"time"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWorkflowsDispatch represents a sub command to dispatch a workflow
var CmdWorkflowsDispatch = cli.Command{
	Name:        "dispatch",
	Aliases:     []string{"trigger", "run"},
	Usage:       "Dispatch a workflow run",
	Description: "Trigger a workflow_dispatch event for a workflow",
	ArgsUsage:   "<workflow-id>",
	Action:      runWorkflowsDispatch,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "ref",
			Aliases: []string{"r"},
			Usage:   "branch or tag to dispatch on (default: current branch)",
		},
		&cli.StringSliceFlag{
			Name:    "input",
			Aliases: []string{"i"},
			Usage:   "workflow input in key=value format (can be specified multiple times)",
		},
		&cli.BoolFlag{
			Name:    "follow",
			Aliases: []string{"f"},
			Usage:   "follow log output after dispatching",
		},
	}, flags.AllDefaultFlags...),
}

func runWorkflowsDispatch(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("workflow ID is required")
	}

	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := c.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := c.Login.Client()

	workflowID := cmd.Args().First()

	ref := cmd.String("ref")
	if ref == "" {
		if c.LocalRepo != nil {
			branchName, _, localErr := c.LocalRepo.TeaGetCurrentBranchNameAndSHA()
			if localErr == nil && branchName != "" {
				ref = branchName
			}
		}
		if ref == "" {
			return fmt.Errorf("--ref is required (no local branch detected)")
		}
	}

	inputs := make(map[string]string)
	for _, input := range cmd.StringSlice("input") {
		key, value, ok := strings.Cut(input, "=")
		if !ok {
			return fmt.Errorf("invalid input format %q, expected key=value", input)
		}
		inputs[key] = value
	}

	opt := gitea.CreateActionWorkflowDispatchOption{
		Ref:    ref,
		Inputs: inputs,
	}

	details, _, err := client.DispatchRepoActionWorkflow(c.Owner, c.Repo, workflowID, opt, true)
	if err != nil {
		return fmt.Errorf("failed to dispatch workflow: %w", err)
	}

	print.ActionWorkflowDispatchResult(details)

	if cmd.Bool("follow") && details != nil && details.WorkflowRunID > 0 {
		return followDispatchedRun(client, c, details.WorkflowRunID)
	}

	return nil
}

const (
	followPollInterval = 2 * time.Second
	followMaxDuration  = 30 * time.Minute
)

// followDispatchedRun waits for the dispatched run to start, then follows its logs
func followDispatchedRun(client *gitea.Client, c *context.TeaContext, runID int64) error {
	fmt.Printf("\nWaiting for run %d to start...\n", runID)

	var jobs *gitea.ActionWorkflowJobsResponse
	for range 30 {
		time.Sleep(followPollInterval)

		var err error
		jobs, _, err = client.ListRepoActionRunJobs(c.Owner, c.Repo, runID, gitea.ListRepoActionJobsOptions{})
		if err != nil {
			return fmt.Errorf("failed to get jobs: %w", err)
		}
		if len(jobs.Jobs) > 0 {
			break
		}
	}

	if jobs == nil || len(jobs.Jobs) == 0 {
		return fmt.Errorf("timed out waiting for jobs to appear")
	}

	jobID := jobs.Jobs[0].ID
	jobName := jobs.Jobs[0].Name
	fmt.Printf("Following logs for job '%s' (ID: %d) - press Ctrl+C to stop...\n", jobName, jobID)
	fmt.Println("---")

	deadline := time.Now().Add(followMaxDuration)
	var lastLogLength int
	for time.Now().Before(deadline) {
		job, _, err := client.GetRepoActionJob(c.Owner, c.Repo, jobID)
		if err != nil {
			return fmt.Errorf("failed to get job: %w", err)
		}

		isRunning := job.Status == "in_progress" || job.Status == "queued" || job.Status == "pending"

		logs, _, logErr := client.GetRepoActionJobLogs(c.Owner, c.Repo, jobID)
		if logErr != nil && isRunning {
			time.Sleep(followPollInterval)
			continue
		}

		if logErr == nil && len(logs) > lastLogLength {
			fmt.Print(string(logs[lastLogLength:]))
			lastLogLength = len(logs)
		}

		if !isRunning {
			if logErr != nil {
				fmt.Printf("\n---\nJob completed with status: %s (failed to fetch final logs: %v)\n", job.Status, logErr)
			} else {
				fmt.Printf("\n---\nJob completed with status: %s\n", job.Status)
			}
			break
		}

		time.Sleep(followPollInterval)
	}

	if time.Now().After(deadline) {
		return fmt.Errorf("timed out after %s following logs", followMaxDuration)
	}

	return nil
}
