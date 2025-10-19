// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// ActionSecretsList prints a list of action secrets
func ActionSecretsList(secrets []*gitea.Secret, output string) {
	t := table{
		headers: []string{
			"Name",
			"Created",
		},
	}

	for _, secret := range secrets {
		t.addRow(
			secret.Name,
			FormatTime(secret.Created, output != ""),
		)
	}

	if len(secrets) == 0 {
		fmt.Printf("No secrets found\n")
		return
	}

	t.sort(0, true)
	t.print(output)
}

// ActionVariableDetails prints details of a specific action variable
func ActionVariableDetails(variable *gitea.RepoActionVariable) {
	fmt.Printf("Name: %s\n", variable.Name)
	fmt.Printf("Value: %s\n", variable.Value)
	fmt.Printf("Repository ID: %d\n", variable.RepoID)
	fmt.Printf("Owner ID: %d\n", variable.OwnerID)
}

// ActionVariablesList prints a list of action variables
func ActionVariablesList(variables []*gitea.RepoActionVariable, output string) {
	t := table{
		headers: []string{
			"Name",
			"Value",
			"Repository ID",
		},
	}

	for _, variable := range variables {
		// Truncate long values for table display
		value := variable.Value
		if len(value) > 50 {
			value = value[:47] + "..."
		}

		t.addRow(
			variable.Name,
			value,
			fmt.Sprintf("%d", variable.RepoID),
		)
	}

	if len(variables) == 0 {
		fmt.Printf("No variables found\n")
		return
	}

	t.sort(0, true)
	t.print(output)
}
