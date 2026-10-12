// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestPullMergeSendsTitleAndMessage(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			require.Equal(t, "/api/v1/repos/alice/project/pulls/3", r.URL.Path)
			_, _ = w.Write([]byte(`{"number":3,"head":{"sha":"abc123"}}`))
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/pulls/3/merge", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--method", "squash",
		"--title", "Squash title", "--message", "Squash body", "--yes",
	}))
	require.Equal(t, "squash", payload["Do"])
	require.Equal(t, "Squash title", payload["MergeTitleField"])
	require.Equal(t, "Squash body", payload["MergeMessageField"])
	require.Equal(t, false, payload["delete_branch_after_merge"])
	require.Equal(t, "abc123", payload["head_commit_id"])

	payload = nil
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--style", "rebase", "--yes",
	}))
	require.Equal(t, "rebase", payload["Do"])
}

func TestPullMergeMessageFileAndEditor(t *testing.T) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"number":3,"head":{"sha":"abc123"}}`))
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	file := t.TempDir() + "/message.md"
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--message-file", file, "--yes",
	}))
	require.Equal(t, "from file", payload["MergeMessageField"])

	deps.Editor, _ = newDraftEditor(t, "from editor")
	require.NoError(t, Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--editor", "--yes", "--no-input",
	}))
	require.Equal(t, "from editor", payload["MergeMessageField"])
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"pr", "merge", "3", "-R", "alice/project", "--message", "x", "--editor", "--yes",
	})))
}

func TestPullMergePromptsToDeleteBranch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		accept, tty   bool
		flags         []string
		want          bool
		confirmations int
	}{
		{"accepted", true, true, nil, true, 2},
		{"explicit flag skips prompt", true, true, []string{"--delete-branch=false"}, false, 1},
		{"no terminal keeps default", true, false, []string{"--yes"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			views := 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/3":
					views++
					_, _ = w.Write([]byte(`{"number":3,"head":{"ref":"feature","sha":"abc123"}}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/3/merge":
					require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			prompt := &scriptedPrompter{accept: tc.accept}
			deps, _, _ := serverDeps(t, server.URL)
			deps.Prompter = prompt
			deps.Streams.InTTY, deps.Streams.ErrTTY = tc.tty, tc.tty
			require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "merge", "3", "-R", "alice/project"}, tc.flags...)))
			require.Equal(t, tc.want, payload["delete_branch_after_merge"])
			require.Len(t, prompt.confirmations, tc.confirmations)
			promptViews := 0
			if tc.confirmations == 2 {
				promptViews = 1
			}
			require.Equal(t, 1+promptViews, views, "the merge reads the current head; the prompt reads the branch")
		})
	}
}
