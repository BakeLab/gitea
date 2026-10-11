// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseBulkAssetInputAndPartialFailures(t *testing.T) {
	uploads := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1"}`))
			return
		}
		uploads++
		if uploads == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"id":2,"name":"second.txt"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	first, second := filepath.Join(t.TempDir(), "first.txt"), filepath.Join(t.TempDir(), "second.txt")
	require.NoError(t, os.WriteFile(first, []byte("first"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("second"), 0o600))
	err := Execute(context.Background(), deps, []string{"release", "asset", "upload", "v1", first, second + "missing", "-R", "owner/repo", "--json"})
	require.Error(t, err)
	require.Zero(t, uploads)
	err = Execute(context.Background(), deps, []string{"release", "asset", "upload", "v1", first, second, "-R", "owner/repo", "--json"})
	require.ErrorContains(t, err, "first.txt")
	require.Equal(t, 2, uploads)
	require.JSONEq(t, `[{"id":2,"name":"second.txt","size":0,"url":"","created_at":"","download_count":0}]`, out.String())
}

func TestReleaseAssetNameSelectionValidatedBeforeDeletion(t *testing.T) {
	var deleted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/assets") {
			_, _ = w.Write([]byte(`[{"id":1,"name":"one.txt"},{"id":2,"name":"two.txt"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "missing", "--by-name", "--yes", "-R", "owner/repo"})
	require.ErrorContains(t, err, "matches 0 assets")
	require.Empty(t, deleted)
	err = Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "two.txt", "--by-name", "--yes", "-R", "owner/repo"})
	require.NoError(t, err)
	require.Equal(t, []string{"/api/v1/repos/owner/repo/releases/1/assets/1", "/api/v1/repos/owner/repo/releases/1/assets/2"}, deleted)
	require.Contains(t, diagnostics.String(), "Deleted asset 1")
	require.Contains(t, diagnostics.String(), "Deleted asset 2")
}
