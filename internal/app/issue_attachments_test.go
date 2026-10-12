// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestAttachmentCommandsShareIssueAssetsAndPagination(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/1/assets":
			_, _ = w.Write([]byte(`[{"id":1,"name":"one"},{"id":2,"name":"two"}]`))
		case "/api/v1/repos/alice/project/issues/1/assets/1":
			_, _ = w.Write([]byte(`{"id":1,"name":"one"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	for _, group := range []string{"issue", "pr"} {
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "list", "1", "--repo", "alice/project", "--limit=1", "--page=2", "--json=id,name"}))
		require.JSONEq(t, `[{"id":2,"name":"two"}]`, out.String())
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "list", "1", "--repo", "alice/project", "--limit=1", "--page=3", "--json"}))
		require.JSONEq(t, `[]`, out.String())
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "view", "1", "1", "--repo", "alice/project", "--json=id,name"}))
		require.JSONEq(t, `{"id":1,"name":"one"}`, out.String())
	}
}

func TestAttachmentRenameAndConfirmedDeletion(t *testing.T) {
	for _, group := range []string{"issue", "pr"} {
		mutations := 0
		server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/v1/repos/alice/project/issues/1/assets/1", r.URL.Path)
			mutations++
			if r.Method == http.MethodPatch {
				var body map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "renamed.txt", body["name"])
				_, _ = w.Write([]byte(`{"id":1,"name":"renamed.txt"}`))
			} else {
				require.Equal(t, http.MethodDelete, r.Method)
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "edit", "1", "1", "--repo", "alice/project", "--name", "renamed.txt", "--json=name"}))
		require.JSONEq(t, `{"name":"renamed.txt"}`, out.String())
		out.Reset()
		err := Execute(context.Background(), deps, []string{group, "attachment", "delete", "1", "1", "--repo", "alice/project", "--no-input"})
		require.Error(t, err)
		require.Equal(t, 1, mutations)
		require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "delete", "1", "1", "--repo", "alice/project", "--yes", "--no-input"}))
		require.Equal(t, 2, mutations)
		require.Empty(t, out.String())
		for _, args := range [][]string{
			{group, "attachment", "edit", "1", "1", "--name="},
			{group, "attachment", "delete", "1", "1", "--json"},
		} {
			require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, args)))
		}
		require.Equal(t, 2, mutations)
		server.Close()
	}
}
