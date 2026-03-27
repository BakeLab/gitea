// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runs

import (
	stdctx "context"
	"fmt"
	"strconv"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdRunsView represents a sub command to view workflow run details
var CmdRunsView = cli.Command{
	Name:        "view",
	Aliases:     []string{"show", "get"},
	Usage:       "View workflow run details",
	Description: "View details of a specific workflow run including jobs",
	ArgsUsage:   "<run-id>",
	Action:      runRunsView,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:  "jobs",
			Usage: "show jobs table",
			Value: true,
		},
	}, flags.AllDefaultFlags...),
}

func runRunsView(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("run ID is required")
	}

	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := c.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := c.Login.Client()

	runIDStr := cmd.Args().First()
	runID, err := strconv.ParseInt(runIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid run ID: %s", runIDStr)
	}

	// Fetch run details
	run, _, err := client.GetRepoActionRun(c.Owner, c.Repo, runID)
	if err != nil {
		return fmt.Errorf("failed to get run: %w", err)
	}

	// Print run details
	print.ActionRunDetails(run)

	// Fetch and print jobs if requested
	if cmd.Bool("jobs") {
		jobs, _, err := client.ListRepoActionRunJobs(c.Owner, c.Repo, runID, gitea.ListRepoActionJobsOptions{
			ListOptions: flags.GetListOptions(cmd),
		})
		if err != nil {
			return fmt.Errorf("failed to get jobs: %w", err)
		}

		if jobs != nil && len(jobs.Jobs) > 0 {
			fmt.Printf("\nJobs:\n\n")
			if err := print.ActionWorkflowJobsList(jobs.Jobs, c.Output); err != nil {
				return err
			}
		}
	}

	return nil
}
