// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package workflows

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWorkflowsList represents a sub command to list workflows
var CmdWorkflowsList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List repository workflows",
	Description: "List workflows in the repository with their status",
	Action:      RunWorkflowsList,
	Flags:       flags.AllDefaultFlags,
}

// RunWorkflowsList lists workflows in the repository using the workflow API
func RunWorkflowsList(ctx stdctx.Context, cmd *cli.Command) error {
	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := c.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := c.Login.Client()

	resp, _, err := client.ListRepoActionWorkflows(c.Owner, c.Repo)
	if err != nil {
		return fmt.Errorf("failed to list workflows: %w", err)
	}

	var workflows []*gitea.ActionWorkflow
	if resp != nil {
		workflows = resp.Workflows
	}

	return print.ActionWorkflowsList(workflows, c.Output)
}
