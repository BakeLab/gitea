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

// CmdWorkflowsEnable represents a sub command to enable a workflow
var CmdWorkflowsEnable = cli.Command{
	Name:        "enable",
	Usage:       "Enable a workflow",
	Description: "Enable a disabled workflow in the repository",
	ArgsUsage:   "<workflow-id>",
	Action:      runWorkflowsEnable,
	Flags:       flags.AllDefaultFlags,
}

func runWorkflowsEnable(ctx stdctx.Context, cmd *cli.Command) error {
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
	_, err = client.EnableRepoActionWorkflow(c.Owner, c.Repo, workflowID)
	if err != nil {
		return fmt.Errorf("failed to enable workflow: %w", err)
	}

	fmt.Printf("Workflow %s enabled successfully\n", workflowID)
	return nil
}
