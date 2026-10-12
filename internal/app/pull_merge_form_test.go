// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type mergeFormFunc func(context.Context, []terminal.MergePullChoice) (int64, error)

func (f mergeFormFunc) SelectPull(ctx context.Context, choices []terminal.MergePullChoice) (int64, error) {
	return f(ctx, choices)
}

func TestPullMergeInteractiveSelectsMatchingBranch(t *testing.T) {
	merges := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.Equal(t, "open", r.URL.Query().Get("state"))
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[
				{"number":3,"title":"Other","state":"open","base":{"ref":"main"},"head":{"ref":"other","label":"alice:other"}},
				{"number":7,"title":"Feature","state":"open","base":{"ref":"main"},"head":{"ref":"feature","label":"alice:feature"}},
				{"number":9,"title":"Into feature","state":"open","base":{"ref":"feature"},"head":{"ref":"topic","label":"alice:topic"}}
			]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"head":{"ref":"feature","sha":"abc123"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls/7/merge":
			merges++
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	calls := 0
	prompt := &scriptedPrompter{accept: true}
	deps, _, _ := serverDeps(t, server.URL)
	deps.MergeForm = mergeFormFunc(func(_ context.Context, choices []terminal.MergePullChoice) (int64, error) {
		calls++
		require.Len(t, choices, 2)
		require.EqualValues(t, 7, choices[0].Number)
		require.EqualValues(t, 9, choices[1].Number)
		return 7, nil
	})
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "feature"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "merge", "-R", "alice/project", "--interactive", "--yes"}))
	require.Equal(t, 1, calls)
	require.Equal(t, 1, merges)
}

func TestPullMergeRequiresNumberWithoutInteractive(t *testing.T) {
	deps, _, _ := testDeps(t)
	deps.MergeForm = mergeFormFunc(func(context.Context, []terminal.MergePullChoice) (int64, error) {
		t.Fatal("merge form should not run")
		return 0, nil
	})
	err := Execute(context.Background(), deps, []string{"pr", "merge", "-R", "alice/project", "--no-input"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull request number")
}
