// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestMilestoneCreateDescriptionFileAndEditor(t *testing.T) {
	var createBody map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createBody))
		_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"from file","state":"open"}`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	file := filepath.Join(t.TempDir(), "description.md")
	require.NoError(t, os.WriteFile(file, []byte("from file"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "create", "-R", "alice/project", "--title", "Ship", "--description-file", file, "--json=id,description", "--no-input"}))
	require.Equal(t, "from file", createBody["description"])
	require.JSONEq(t, `{"id":4,"description":"from file"}`, out.String())

	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"milestone", "create", "-R", "alice/project", "--title", "Ship", "--description", "x", "--description-file", file, "--no-input",
	})))
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{
		"milestone", "edit", "4", "-R", "alice/project", "--editor", "--description", "x", "--no-input",
	})))
}

func TestMilestoneEditEditorSeedsDescription(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/milestones/4":
			_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"current notes","state":"open"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/milestones/4":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "edited notes", body["description"])
			_, _ = w.Write([]byte(`{"id":4,"title":"Ship","description":"edited notes","state":"open"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	editor, path := newDraftEditor(t, "edited notes")
	deps.Editor = editor
	deps.Getenv = serverEnv(server.URL, map[string]string{"EDITOR": "chosen-editor"})
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "edit", "4", "-R", "alice/project", "--editor", "--json=description", "--no-input"}))
	require.Equal(t, "current notes", editor.initial)
	require.JSONEq(t, `{"description":"edited notes"}`, out.String())
	requireDraft(t, path, false)
}
