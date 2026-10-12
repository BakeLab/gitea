// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"os"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestPullMergeSendsTitleAndMessage(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--method", "squash",
		"--title", "Squash title", "--message", "Squash body", "--delete-branch=false", "--yes",
	}))
	require.Equal(t, "squash", payload["Do"])
	require.Equal(t, "Squash title", payload["MergeTitleField"])
	require.Equal(t, "Squash body", payload["MergeMessageField"])
	require.Equal(t, false, payload["delete_branch_after_merge"])
	require.Equal(t, "abc123", payload["head_commit_id"])

	payload = nil
	server = newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--style", "rebase", "--yes"}))
	require.Equal(t, "rebase", payload["Do"])
}

func TestPullMergeMessageFileAndEditor(t *testing.T) {
	var payload map[string]any
	deps, _, _ := testDeps(t)
	file := t.TempDir() + "/message.md"
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--message-file", file, "--yes"}))
	require.Equal(t, "from file", payload["MergeMessageField"])

	server = newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	deps.Editor, _ = newDraftEditor(t, "from editor")
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--editor", "--yes", "--no-input"}))
	require.Equal(t, "from editor", payload["MergeMessageField"])
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--message", "x", "--editor", "--yes",
	})))
}

func TestPullMergeReturnsTheMergedPullRequest(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(mergeServer(t, openPull, &payload))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "3", "-R", "alice/project", "--yes", "--json", "number,merged"}))
	require.JSONEq(t, `{"number":3,"merged":true}`, out.String())
}
