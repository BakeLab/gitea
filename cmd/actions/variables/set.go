// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package variables

import (
	stdctx "context"
	"fmt"
	"regexp"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdVariablesSet represents a sub command to set action variables
var CmdVariablesSet = cli.Command{
	Name:        "set",
	Aliases:     []string{"create", "update"},
	Usage:       "Set an action variable",
	Description: "Set a variable for use in repository actions and workflows",
	ArgsUsage:   "<variable-name> [variable-value]",
	Action:      runVariablesSet,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "file",
			Usage: "read variable value from file",
		},
		&cli.BoolFlag{
			Name:  "stdin",
			Usage: "read variable value from stdin",
		},
	}, flags.AllDefaultFlags...),
}

func runVariablesSet(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("variable name is required")
	}

	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := c.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := c.Login.Client()

	variableName := cmd.Args().First()
	if err := validateVariableName(variableName); err != nil {
		return err
	}

	// Read variable value using the utility
	variableValue, err := utils.ReadValue(cmd, utils.ReadValueOptions{
		ResourceName: "variable",
		PromptMsg:    fmt.Sprintf("Enter variable value for '%s'", variableName),
		Hidden:       false,
		AllowEmpty:   false,
	})
	if err != nil {
		return err
	}

	if err := validateVariableValue(variableValue); err != nil {
		return err
	}

	_, err = client.CreateRepoActionVariable(c.Owner, c.Repo, variableName, variableValue)
	if err != nil {
		return err
	}

	fmt.Printf("Variable '%s' set successfully\n", variableName)
	return nil
}

// validateVariableName validates that a variable name follows the required format
func validateVariableName(name string) error {
	if name == "" {
		return fmt.Errorf("variable name cannot be empty")
	}

	// Variable names can contain letters (upper/lower), numbers, and underscores
	// Cannot start with a number
	// Cannot contain spaces or special characters (except underscore)
	validPattern := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	if !validPattern.MatchString(name) {
		return fmt.Errorf("variable name must contain only letters, numbers, and underscores, and cannot start with a number")
	}

	return nil
}

// validateVariableValue validates that a variable value is acceptable
func validateVariableValue(value string) error {
	// Variables can be empty or contain whitespace, unlike secrets

	// Check for maximum size (64KB limit)
	if len(value) > 65536 {
		return fmt.Errorf("variable value cannot exceed 64KB")
	}

	return nil
}
