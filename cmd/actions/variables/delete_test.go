// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package variables

import (
	"fmt"
	"testing"
)

func TestVariablesDeleteValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid variable name",
			args:    []string{"VALID_VARIABLE"},
			wantErr: false,
		},
		{
			name:    "valid lowercase name",
			args:    []string{"valid_variable"},
			wantErr: false,
		},
		{
			name:    "no args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many args",
			args:    []string{"VARIABLE1", "VARIABLE2"},
			wantErr: true,
		},
		{
			name:    "invalid variable name",
			args:    []string{"invalid-variable"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVariableDeleteArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVariableDeleteArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVariablesDeleteFlags(t *testing.T) {
	cmd := CmdVariablesDelete

	// Test command properties
	if cmd.Name != "delete" {
		t.Errorf("Expected command name 'delete', got %s", cmd.Name)
	}

	// Check that rm is one of the aliases
	hasRmAlias := false
	for _, alias := range cmd.Aliases {
		if alias == "rm" {
			hasRmAlias = true
			break
		}
	}
	if !hasRmAlias {
		t.Error("Expected 'rm' to be one of the aliases for delete command")
	}

	if cmd.ArgsUsage != "<variable-name>" {
		t.Errorf("Expected ArgsUsage '<variable-name>', got %s", cmd.ArgsUsage)
	}

	if cmd.Usage == "" {
		t.Error("Delete command should have usage text")
	}

	if cmd.Description == "" {
		t.Error("Delete command should have description")
	}
}

// validateVariableDeleteArgs validates arguments for the delete command
func validateVariableDeleteArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("variable name is required")
	}

	if len(args) > 1 {
		return fmt.Errorf("only one variable name allowed")
	}

	return validateVariableName(args[0])
}
