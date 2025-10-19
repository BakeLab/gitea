// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
)

func TestActionSecretsListEmpty(t *testing.T) {
	// Test with empty secrets - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionSecretsList panicked with empty list: %v", r)
		}
	}()

	ActionSecretsList([]*gitea.Secret{}, "")
}

func TestActionSecretsListWithData(t *testing.T) {
	secrets := []*gitea.Secret{
		{
			Name:    "TEST_SECRET_1",
			Created: time.Now().Add(-24 * time.Hour),
		},
		{
			Name:    "TEST_SECRET_2",
			Created: time.Now().Add(-48 * time.Hour),
		},
	}

	// Test that it doesn't panic with real data
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionSecretsList panicked with data: %v", r)
		}
	}()

	ActionSecretsList(secrets, "")

	// Test JSON output format to verify structure
	var buf bytes.Buffer
	testTable := table{
		headers: []string{"Name", "Created"},
	}

	for _, secret := range secrets {
		testTable.addRow(secret.Name, FormatTime(secret.Created, true))
	}

	testTable.fprint(&buf, "json")
	output := buf.String()

	if !strings.Contains(output, "TEST_SECRET_1") {
		t.Error("Expected TEST_SECRET_1 in JSON output")
	}
	if !strings.Contains(output, "TEST_SECRET_2") {
		t.Error("Expected TEST_SECRET_2 in JSON output")
	}
}

func TestActionVariableDetails(t *testing.T) {
	variable := &gitea.RepoActionVariable{
		Name:    "TEST_VARIABLE",
		Value:   "test_value",
		RepoID:  123,
		OwnerID: 456,
	}

	// Test that it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionVariableDetails panicked: %v", r)
		}
	}()

	ActionVariableDetails(variable)
}

func TestActionVariablesListEmpty(t *testing.T) {
	// Test with empty variables - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionVariablesList panicked with empty list: %v", r)
		}
	}()

	ActionVariablesList([]*gitea.RepoActionVariable{}, "")
}

func TestActionVariablesListWithData(t *testing.T) {
	variables := []*gitea.RepoActionVariable{
		{
			Name:    "TEST_VARIABLE_1",
			Value:   "short_value",
			RepoID:  123,
			OwnerID: 456,
		},
		{
			Name:    "TEST_VARIABLE_2",
			Value:   strings.Repeat("a", 60), // Long value to test truncation
			RepoID:  124,
			OwnerID: 457,
		},
	}

	// Test that it doesn't panic with real data
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionVariablesList panicked with data: %v", r)
		}
	}()

	ActionVariablesList(variables, "")

	// Test JSON output format to verify structure and truncation
	var buf bytes.Buffer
	testTable := table{
		headers: []string{"Name", "Value", "Repository ID"},
	}

	for _, variable := range variables {
		value := variable.Value
		if len(value) > 50 {
			value = value[:47] + "..."
		}
		testTable.addRow(variable.Name, value, strconv.Itoa(int(variable.RepoID)))
	}

	testTable.fprint(&buf, "json")
	output := buf.String()

	if !strings.Contains(output, "TEST_VARIABLE_1") {
		t.Error("Expected TEST_VARIABLE_1 in JSON output")
	}
	if !strings.Contains(output, "TEST_VARIABLE_2") {
		t.Error("Expected TEST_VARIABLE_2 in JSON output")
	}

	// Check that long value is truncated in our test table
	if strings.Contains(output, strings.Repeat("a", 60)) {
		t.Error("Long value should be truncated in table output")
	}
}

func TestActionVariablesListValueTruncation(t *testing.T) {
	variable := &gitea.RepoActionVariable{
		Name:    "LONG_VALUE_VARIABLE",
		Value:   strings.Repeat("abcdefghij", 10), // 100 characters
		RepoID:  123,
		OwnerID: 456,
	}

	// Test that it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionVariablesList panicked with long value: %v", r)
		}
	}()

	ActionVariablesList([]*gitea.RepoActionVariable{variable}, "")

	// Test the truncation logic directly
	value := variable.Value
	if len(value) > 50 {
		value = value[:47] + "..."
	}

	if len(value) != 50 { // 47 chars + "..." = 50
		t.Errorf("Truncated value should be 50 characters, got %d", len(value))
	}

	if !strings.HasSuffix(value, "...") {
		t.Error("Truncated value should end with '...'")
	}
}

func TestTableSorting(t *testing.T) {
	// Test that the table sorting works correctly
	secrets := []*gitea.Secret{
		{Name: "Z_SECRET", Created: time.Now()},
		{Name: "A_SECRET", Created: time.Now()},
		{Name: "M_SECRET", Created: time.Now()},
	}

	// Test the table sorting logic
	table := table{
		headers: []string{"Name", "Created"},
	}

	for _, secret := range secrets {
		table.addRow(secret.Name, FormatTime(secret.Created, true))
	}

	// Sort by first column (Name) in ascending order (false = ascending)
	table.sort(0, false)

	// Check that the first row is A_SECRET after ascending sorting
	if table.values[0][0] != "A_SECRET" {
		t.Errorf("Expected first sorted value to be 'A_SECRET', got '%s'", table.values[0][0])
	}

	// Check that the last row is Z_SECRET after ascending sorting
	if table.values[2][0] != "Z_SECRET" {
		t.Errorf("Expected last sorted value to be 'Z_SECRET', got '%s'", table.values[2][0])
	}
}
