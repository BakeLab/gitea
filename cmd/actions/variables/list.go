// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package variables

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"github.com/urfave/cli/v3"
)

// CmdVariablesList represents a sub command to list action variables
var CmdVariablesList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List action variables",
	Description: "List variables configured for repository actions",
	Action:      RunVariablesList,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "name",
			Usage: "show specific variable by name",
		},
	}, flags.AllDefaultFlags...),
}

// RunVariablesList list action variables
func RunVariablesList(ctx stdctx.Context, cmd *cli.Command) error {
	c := context.InitCommand(cmd)
	client := c.Login.Client()

	if name := cmd.String("name"); name != "" {
		// Get specific variable
		variable, _, err := client.GetRepoActionVariable(c.Owner, c.Repo, name)
		if err != nil {
			return err
		}

		print.ActionVariableDetails(variable)
		return nil
	}

	// List all variables - Note: SDK doesn't have ListRepoActionVariables yet
	// This is a limitation of the current SDK
	fmt.Println("Note: Listing all variables is not yet supported by the Gitea SDK.")
	fmt.Println("Use 'tea actions variables list --name <variable-name>' to get a specific variable.")
	fmt.Println("You can also check your repository's Actions settings in the web interface.")

	return nil
}
