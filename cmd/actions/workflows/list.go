// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package workflows

import (
	stdctx "context"
	"fmt"
	"path/filepath"
	"strings"

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
	Description: "List workflow files in the repository with active/inactive status",
	Action:      RunWorkflowsList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunWorkflowsList lists workflow files in the repository
func RunWorkflowsList(ctx stdctx.Context, cmd *cli.Command) error {
	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := c.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := c.Login.Client()

	// Try to list workflow files from .gitea/workflows directory
	var workflows []*gitea.ContentsResponse

	// Try .gitea/workflows first, then .github/workflows
	workflowDir := ".gitea/workflows"
	contents, _, err := client.ListContents(c.Owner, c.Repo, "", workflowDir)
	if err != nil {
		workflowDir = ".github/workflows"
		contents, _, err = client.ListContents(c.Owner, c.Repo, "", workflowDir)
		if err != nil {
			fmt.Printf("No workflow files found\n")
			return nil
		}
	}

	// Filter for workflow files (.yml and .yaml)
	for _, content := range contents {
		if content.Type == "file" {
			ext := strings.ToLower(filepath.Ext(content.Name))
			if ext == ".yml" || ext == ".yaml" {
				content.Path = workflowDir + "/" + content.Name
				workflows = append(workflows, content)
			}
		}
	}

	if len(workflows) == 0 {
		fmt.Printf("No workflow files found\n")
		return nil
	}

	// Check which workflows have runs to determine active status
	workflowStatus := make(map[string]bool)

	// Get recent runs to check activity
	runs, _, err := client.ListRepoActionRuns(c.Owner, c.Repo, gitea.ListRepoActionRunsOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err == nil && runs != nil {
		for _, run := range runs.WorkflowRuns {
			// Extract workflow file name from path
			workflowFile := filepath.Base(run.Path)
			workflowStatus[workflowFile] = true
		}
	}

	return print.WorkflowsList(workflows, workflowStatus, c.Output)
}
