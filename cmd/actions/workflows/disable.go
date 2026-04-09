// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package workflows

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdWorkflowsDisable represents a sub command to disable a workflow
var CmdWorkflowsDisable = cli.Command{
	Name:        "disable",
	Usage:       "Disable a workflow",
	Description: "Disable a workflow in the repository",
	ArgsUsage:   "<workflow-id>",
	Action:      runWorkflowsDisable,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm disable without prompting",
		},
	}, flags.AllDefaultFlags...),
}

func runWorkflowsDisable(ctx stdctx.Context, cmd *cli.Command) error {
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

	if !cmd.Bool("confirm") {
		fmt.Printf("Are you sure you want to disable workflow %s? [y/N] ", workflowID)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Disable canceled.")
			return nil
		}
	}

	_, err = client.DisableRepoActionWorkflow(c.Owner, c.Repo, workflowID)
	if err != nil {
		return fmt.Errorf("failed to disable workflow: %w", err)
	}

	fmt.Printf("Workflow %s disabled successfully\n", workflowID)
	return nil
}
