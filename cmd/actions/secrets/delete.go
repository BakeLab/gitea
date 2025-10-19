// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdSecretsDelete represents a sub command to delete action secrets
var CmdSecretsDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"remove", "rm"},
	Usage:       "Delete an action secret",
	Description: "Delete a secret used by repository actions",
	ArgsUsage:   "<secret-name>",
	Action:      runSecretsDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm deletion without prompting",
		},
	}, flags.AllDefaultFlags...),
}

func runSecretsDelete(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("secret name is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	secretName := cmd.Args().First()

	if !cmd.Bool("confirm") {
		fmt.Printf("Are you sure you want to delete secret '%s'? [y/N] ", secretName)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	_, err := client.DeleteRepoActionSecret(c.Owner, c.Repo, secretName)
	if err != nil {
		return err
	}

	fmt.Printf("Secret '%s' deleted successfully\n", secretName)
	return nil
}
