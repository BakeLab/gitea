// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runs

import (
	stdctx "context"
	"fmt"
	"strconv"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdRunsDelete represents a sub command to delete/cancel workflow runs
var CmdRunsDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"remove", "rm", "cancel"},
	Usage:       "Delete or cancel a workflow run",
	Description: "Delete (cancel) a workflow run from the repository",
	ArgsUsage:   "<run-id>",
	Action:      runRunsDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm deletion without prompting",
		},
	}, flags.AllDefaultFlags...),
}

func runRunsDelete(ctx stdctx.Context, cmd *cli.Command) error {
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

	if !cmd.Bool("confirm") {
		fmt.Printf("Are you sure you want to delete run %d? [y/N] ", runID)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Deletion canceled.")
			return nil
		}
	}

	_, err = client.DeleteRepoActionRun(c.Owner, c.Repo, runID)
	if err != nil {
		return fmt.Errorf("failed to delete run: %w", err)
	}

	fmt.Printf("Run %d deleted successfully\n", runID)
	return nil
}
