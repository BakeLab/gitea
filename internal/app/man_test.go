// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestManWritesSelectedCommandWithoutReplacingFiles(t *testing.T) {
	deps, out, _ := testDeps(t)
	path := filepath.Join(t.TempDir(), "checkout.1")
	args := []string{"man", "pr", "checkout", "--output-file", path}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Empty(t, out.String())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "gt-pr-checkout")
	require.Contains(t, string(data), "detach")
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, args)))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, after)
}

func TestManRejectsUnknownCommandsAndMachineOutput(t *testing.T) {
	for _, args := range [][]string{{"man", "unknown"}, {"man", "pr", "unknown"}, {"man", "--json"}, {"man", "--jq", "."}} {
		deps, out, _ := testDeps(t)
		require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, args)))
		require.Empty(t, out.String())
	}
}

func TestManIncludesFullCommandTree(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"man"}))
	manual := out.String()
	for _, command := range []string{"gt-pr-checkout", "gt-issue-comment-create", "gt-auth-login", "gt-run-artifact-list"} {
		require.Contains(t, manual, command)
	}
	require.Contains(t, manual, `.TH "GT-PR-CHECKOUT" "1" "Jan 2026"`)
	require.NotContains(t, manual, "gt-auth-git-credential")
	first := manual
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"man"}))
	require.Equal(t, first, out.String())
	out.Reset()
	deps.Getenv = envFunc(map[string]string{"SOURCE_DATE_EPOCH": "1780000000"})
	require.NoError(t, Execute(context.Background(), deps, []string{"man", "pr", "checkout"}))
	require.Contains(t, out.String(), `.TH "GT-PR-CHECKOUT" "1" "May 2026"`)
}

func TestCanceledManDoesNotPublishOutputFile(t *testing.T) {
	deps, out, _ := testDeps(t)
	path := filepath.Join(t.TempDir(), "manual.1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, Execute(ctx, deps, []string{"man", "--output-file", path}), context.Canceled)
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))
	require.Empty(t, out.String())
}
