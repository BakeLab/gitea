// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestIssueBulkEditContinuesAfterIndividualFailure(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		calls = append(calls, r.URL.Path)
		var options services.IssueEdit
		require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
		require.NotNil(t, options.Title)
		require.Equal(t, "Updated", *options.Title)
		if strings.HasSuffix(r.URL.Path, "/2") {
			http.Error(w, `{"message":"permission denied"}`, http.StatusForbidden)
			return
		}
		n := "1"
		if strings.HasSuffix(r.URL.Path, "/3") {
			n = "3"
		}
		_, _ = w.Write([]byte(`{"number":` + n + `,"title":"Updated"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"issue", "edit", "1", "2", "3", "--repo", "alice/project", "--title", "Updated", "--no-input", "--json=number,title"})
	require.ErrorContains(t, err, "issue 2")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `[{"number":1,"title":"Updated"},{"number":3,"title":"Updated"}]`, out.String())
	require.Len(t, calls, 3)
	require.Empty(t, diagnostics.String())
}

func TestIssueEditMergesAssigneesWithOneRead(t *testing.T) {
	reads := 0
	var patched []any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reads++
			_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"},{"login":"bob"}]}`))
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		patched = body["assignees"].([]any)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "7", "-R", "alice/project", "--add-assignees", "carol,carol", "--remove-assignees", "bob", "--json"}))
	require.Equal(t, 1, reads)
	require.Equal(t, []any{"alice", "carol"}, patched)
}
