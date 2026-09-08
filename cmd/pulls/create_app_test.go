// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls_test

import (
	"context"
	"testing"

	"gitea.dev/tea/cmd"
	"gitea.dev/tea/modules/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPullsCreateAgitOutputRejected verifies that --output (parsed via the
// urfave/cli v3 ancestor-flag cascade, since create itself does not declare
// it) is rejected for the agit flow before any API call or git push happens.
func TestPullsCreateAgitOutputRejected(t *testing.T) {
	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "testLogin",
			URL:     "https://gitea.example.com",
			Token:   "test-token",
			User:    "testUser",
			Default: true,
		}},
	})
	t.Cleanup(func() {
		config.SetConfigForTesting(config.LocalConfig{})
	})

	app := cmd.App()
	args := []string{
		"tea", "pulls", "create",
		"--agit",
		"--output", "json",
		"--head", "topic-branch",
		"--title", "test",
		"--login", "testLogin",
		"--repo", "user/repo",
	}

	err := app.Run(context.Background(), args)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output cannot be combined with --agit")
}
