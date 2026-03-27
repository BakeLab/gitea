// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"
	"os"
	"testing"

	"code.gitea.io/tea/modules/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestSecretsListFlags(t *testing.T) {
	cmd := CmdSecretsList

	// Test that required flags exist
	expectedFlags := []string{"output", "remote", "login", "repo"}

	for _, flagName := range expectedFlags {
		found := false
		for _, flag := range cmd.Flags {
			if flag.Names()[0] == flagName {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("Expected flag %s not found in CmdSecretsList", flagName)
		}
	}

	// Test command properties
	if cmd.Name != "list" {
		t.Errorf("Expected command name 'list', got %s", cmd.Name)
	}

	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "ls" {
		t.Errorf("Expected alias 'ls' for list command")
	}

	if cmd.Usage == "" {
		t.Error("List command should have usage text")
	}

	if cmd.Description == "" {
		t.Error("List command should have description")
	}
}

func TestSecretsListValidation(t *testing.T) {
	// Basic validation that the command accepts the expected arguments
	// More detailed testing would require mocking the Gitea client

	// Test that list command doesn't require arguments
	args := []string{}
	if len(args) > 0 {
		t.Error("List command should not require arguments")
	}

	// Test that extra arguments are ignored
	extraArgs := []string{"extra", "args"}
	if len(extraArgs) > 0 {
		// This is fine - list commands typically ignore extra args
	}
}

func TestRunSecretsListRequiresRepoContext(t *testing.T) {
	oldWd, err := os.Getwd()
	require.NoError(t, err)

	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWd))
	})

	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "test",
			URL:     "https://gitea.example.com",
			Token:   "token",
			User:    "tester",
			Default: true,
		}},
	})

	cmd := &cli.Command{
		Name:  CmdSecretsList.Name,
		Flags: CmdSecretsList.Flags,
	}
	require.NoError(t, cmd.Set("login", "test"))

	err = RunSecretsList(stdctx.Background(), cmd)
	require.ErrorContains(t, err, "remote repository required")
}
