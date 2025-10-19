// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
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
	var secretValue string

	// Determine how to get the secret value
	if cmd.String("file") != "" {
		// Read from file
		content, err := os.ReadFile(cmd.String("file"))
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}
		secretValue = strings.TrimSpace(string(content))
	} else if cmd.Bool("stdin") {
		// Read from stdin
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}
		secretValue = strings.TrimSpace(string(content))
	} else if cmd.Args().Len() >= 2 {
		// Use provided argument
		secretValue = cmd.Args().Get(1)
	} else {
		// Interactive prompt (hidden input)
		fmt.Printf("Enter secret value for '%s': ", secretName)
		byteValue, err := term.ReadPassword(int(syscall.Stdin))
		if err != nil {
			return fmt.Errorf("failed to read secret value: %w", err)
		}
		fmt.Println() // Add newline after hidden input
		secretValue = string(byteValue)
	}

	if secretValue == "" {
		return fmt.Errorf("secret value cannot be empty")
	}

	_, err := client.CreateRepoActionSecret(c.Owner, c.Repo, gitea.CreateSecretOption{
		Name: secretName,
		Data: secretValue,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Secret '%s' created successfully\n", secretName)
	return nil
}
