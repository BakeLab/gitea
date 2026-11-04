// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestErrorFormatEnvironment(t *testing.T) {
	for value, want := range map[string]bool{"": false, "text": false, "json": true, " json ": true} {
		deps, _, _ := testDeps(t)
		deps.Getenv = envFunc(map[string]string{errorFormatEnv: value})
		require.Equal(t, want, JSONErrors(deps), value)
		require.NoError(t, Execute(context.Background(), deps, []string{"alias", "list"}), value)
	}
	deps, _, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{errorFormatEnv: "yaml"})
	err := Execute(context.Background(), deps, []string{"alias", "list"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `invalid GITEA_CLI_ERROR_FORMAT "yaml"`)
}
