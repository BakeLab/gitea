// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"fmt"
	"testing"
)

func TestSecretsDeleteValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid secret name",
			args:    []string{"VALID_SECRET"},
			wantErr: false,
		},
		{
			name:    "no args",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many args",
			args:    []string{"SECRET1", "SECRET2"},
			wantErr: true,
		},
		{
			name:    "invalid secret name but client does not validate",
			args:    []string{"invalid_secret"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDeleteArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDeleteArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecretsDeleteFlags(t *testing.T) {
	cmd := CmdSecretsDelete

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

	if cmd.ArgsUsage != "<secret-name>" {
		t.Errorf("Expected ArgsUsage '<secret-name>', got %s", cmd.ArgsUsage)
	}

	if cmd.Usage == "" {
		t.Error("Delete command should have usage text")
	}

	if cmd.Description == "" {
		t.Error("Delete command should have description")
	}
}

// validateDeleteArgs validates arguments for the delete command
func validateDeleteArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("secret name is required")
	}

	if len(args) > 1 {
		return fmt.Errorf("only one secret name allowed")
	}

	return nil
}
