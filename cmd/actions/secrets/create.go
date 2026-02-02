// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdSecretsCreate represents a sub command to create action secrets
var CmdSecretsCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"add", "set"},
	Usage:       "Create an action secret",
	Description: "Create a secret for use in repository actions and workflows",
	ArgsUsage:   "<secret-name> [secret-value]",
	Action:      runSecretsCreate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "file",
			Usage: "read secret value from file",
		},
		&cli.BoolFlag{
			Name:  "stdin",
			Usage: "read secret value from stdin",
		},
	}, flags.AllDefaultFlags...),
}

func runSecretsCreate(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("secret name is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	secretName := cmd.Args().First()

	// Read secret value using the utility
	secretValue, err := utils.ReadValue(cmd, utils.ReadValueOptions{
		ResourceName: "secret",
		PromptMsg:    fmt.Sprintf("Enter secret value for '%s'", secretName),
		Hidden:       true,
		AllowEmpty:   false,
	})
	if err != nil {
		return err
	}

	_, err = client.CreateRepoActionSecret(c.Owner, c.Repo, gitea.CreateSecretOption{
		Name: secretName,
		Data: secretValue,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Secret '%s' created successfully\n", secretName)
	return nil
}
