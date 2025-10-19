// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package variables

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdVariablesDelete represents a sub command to delete action variables
var CmdVariablesDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"remove", "rm"},
	Usage:       "Delete an action variable",
	Description: "Delete a variable used by repository actions",
	ArgsUsage:   "<variable-name>",
	Action:      runVariablesDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm deletion without prompting",
		},
	}, flags.AllDefaultFlags...),
}

func runVariablesDelete(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("variable name is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	variableName := cmd.Args().First()

	if !cmd.Bool("confirm") {
		fmt.Printf("Are you sure you want to delete variable '%s'? [y/N] ", variableName)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	_, err := client.DeleteRepoActionVariable(c.Owner, c.Repo, variableName)
	if err != nil {
		return err
	}

	fmt.Printf("Variable '%s' deleted successfully\n", variableName)
	return nil
}
