// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package workflows

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"github.com/urfave/cli/v3"
)

// CmdWorkflowsView represents a sub command to view workflow details
var CmdWorkflowsView = cli.Command{
	Name:        "view",
	Aliases:     []string{"show", "get"},
	Usage:       "View workflow details",
	Description: "View details of a specific workflow",
	ArgsUsage:   "<workflow-id>",
	Action:      runWorkflowsView,
	Flags:       flags.AllDefaultFlags,
}

func runWorkflowsView(ctx stdctx.Context, cmd *cli.Command) error {
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
	wf, _, err := client.GetRepoActionWorkflow(c.Owner, c.Repo, workflowID)
	if err != nil {
		return fmt.Errorf("failed to get workflow: %w", err)
	}

	print.ActionWorkflowDetails(wf)
	return nil
}
