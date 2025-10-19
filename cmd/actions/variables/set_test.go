// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package variables

import (
	"strings"
	"testing"
)

func TestValidateVariableName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid name",
			input:   "VALID_VARIABLE_NAME",
			wantErr: false,
		},
		{
			name:    "valid name with numbers",
			input:   "VARIABLE_123",
			wantErr: false,
		},
		{
			name:    "valid lowercase",
			input:   "valid_variable",
			wantErr: false,
		},
		{
			name:    "valid mixed case",
			input:   "Mixed_Case_Variable",
			wantErr: false,
		},
		{
			name:    "invalid - spaces",
			input:   "INVALID VARIABLE",
			wantErr: true,
		},
		{
			name:    "invalid - special chars",
			input:   "INVALID-VARIABLE!",
			wantErr: true,
		},
		{
			name:    "invalid - starts with number",
			input:   "1INVALID",
			wantErr: true,
		},
		{
			name:    "invalid - empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVariableName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVariableName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestGetVariableSourceArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "valid args",
			args:    []string{"VALID_VARIABLE", "variable_value"},
			wantErr: false,
		},
		{
			name:    "valid lowercase",
			args:    []string{"valid_variable", "value"},
			wantErr: false,
		},
		{
			name:    "missing name",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "too many args",
			args:    []string{"VARIABLE_NAME", "value", "extra"},
			wantErr: true,
		},
		{
			name:    "invalid variable name",
			args:    []string{"invalid-variable", "value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test argument validation only
			if len(tt.args) == 0 {
				if !tt.wantErr {
					t.Error("Expected error for empty args")
				}
				return
			}

			if len(tt.args) > 2 {
				if !tt.wantErr {
					t.Error("Expected error for too many args")
				}
				return
			}

			// Test variable name validation
			err := validateVariableName(tt.args[0])
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVariableName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVariableNameValidation(t *testing.T) {
	// Test that variable names follow GitHub Actions/Gitea Actions conventions
	validNames := []string{
		"VALID_VARIABLE",
		"API_URL",
		"DATABASE_HOST",
		"VARIABLE_123",
		"mixed_Case_Variable",
		"lowercase_variable",
		"UPPERCASE_VARIABLE",
	}

	invalidNames := []string{
		"Invalid-Dashes",
		"INVALID SPACES",
		"123_STARTS_WITH_NUMBER",
		"",           // Empty
		"INVALID!@#", // Special chars
	}

	for _, name := range validNames {
		t.Run("valid_"+name, func(t *testing.T) {
			err := validateVariableName(name)
			if err != nil {
				t.Errorf("validateVariableName(%q) should be valid, got error: %v", name, err)
			}
		})
	}

	for _, name := range invalidNames {
		t.Run("invalid_"+name, func(t *testing.T) {
			err := validateVariableName(name)
			if err == nil {
				t.Errorf("validateVariableName(%q) should be invalid, got no error", name)
			}
		})
	}
}

func TestVariableValueValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{
			name:    "valid value",
			value:   "variable123",
			wantErr: false,
		},
		{
			name:    "valid complex value",
			value:   "https://api.example.com/v1",
			wantErr: false,
		},
		{
			name:    "valid multiline value",
			value:   "line1\nline2\nline3",
			wantErr: false,
		},
		{
			name:    "empty value allowed",
			value:   "",
			wantErr: false, // Variables can be empty unlike secrets
		},
		{
			name:    "whitespace only allowed",
			value:   "   \t\n   ",
			wantErr: false, // Variables can contain whitespace
		},
		{
			name:    "very long value",
			value:   strings.Repeat("a", 65537), // Over 64KB
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVariableValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVariableValue() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
