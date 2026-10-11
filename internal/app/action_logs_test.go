// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunLogsStreamWithoutMixingDiagnosticsAndVerifyJobMembership(t *testing.T) {
	downloads := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/7/jobs":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"jobs":[{"id":9,"name":"Build"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
			}
		case "/api/v1/repos/owner/repo/actions/runs/7/logs":
			_, _ = w.Write([]byte("ZIP archive bytes"))
		case "/api/v1/repos/owner/repo/actions/jobs/9/logs":
			downloads++
			_, _ = w.Write([]byte("exact log\n"))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	args := []string{"run", "logs", "7", "-R", "owner/repo", "--job=9", "--no-input"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "exact log\n", out.String())
	require.Equal(t, "Job 9: Build\n", diagnostics.String())
	require.Equal(t, 1, downloads)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "7", "-R", "owner/repo", "--limit=20", "--page=1", "--json=id,name,run_attempt"}))
	require.JSONEq(t, `[{"id":9,"name":"Build","run_attempt":0}]`, out.String())
	require.Empty(t, diagnostics.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "7", "-R", "owner/repo", "--page=2", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	out.Reset()
	diagnostics.Reset()
	args[5] = "--job=10"
	err := Execute(context.Background(), deps, args)
	require.ErrorContains(t, err, "does not belong")
	require.Equal(t, 1, downloads)
	require.Empty(t, out.String())
	path := filepath.Join(t.TempDir(), "logs.zip")
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--output-file", path}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "ZIP archive bytes", string(data))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}
