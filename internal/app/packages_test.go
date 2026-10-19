// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackagesListViewDeleteLinkAndUpload(t *testing.T) {
	var requests []string
	uploaded := map[string]string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/packages/acme":
			require.Equal(t, "generic", r.URL.Query().Get("type"))
			writeFirstPage(w, r, `[{"id":1,"type":"generic","name":"tool","version":"1.0.0","owner":{"login":"acme"},"repository":{"full_name":"acme/tool"},"created_at":"2026-01-02T00:00:00Z"}]`)
		case "GET /api/v1/packages/acme/generic/tool/-/latest":
			_, _ = w.Write([]byte(`{"id":1,"type":"generic","name":"tool","version":"1.0.0"}`))
		case "GET /api/v1/packages/acme/generic/tool/1.0.0/files":
			_, _ = w.Write([]byte(`[{"id":7,"name":"tool-linux","size":2048,"sha256":"abc"}]`))
		case "DELETE /api/v1/packages/acme/generic/tool/1.0.0", "DELETE /api/v1/packages/acme/generic/tool",
			"POST /api/v1/packages/acme/generic/tool/-/link/tool-source", "POST /api/v1/packages/acme/generic/tool/-/unlink":
			w.WriteHeader(http.StatusNoContent)
		case "PUT /api/packages/acme/generic/tool/2.0.0/tool-linux":
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			uploaded[r.URL.Path] = string(data)
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "acme/tool")))
		return out.String()
	}
	require.Equal(t, "generic\ttool\t1.0.0\tacme/tool\t2026-01-02T00:00:00Z\n", run("package", "list", "--type", "generic"))
	require.JSONEq(t, `{"version":"1.0.0","files":[{"id":7,"name":"tool-linux","size":2048,"sha256":"abc"}]}`, run("package", "view", "generic", "tool", "--json", "version,files"))
	run("package", "delete", "generic", "tool", "1.0.0", "--yes")
	run("package", "delete", "generic", "tool", "--all", "--yes")
	run("package", "link", "generic", "tool", "tool-source")
	run("package", "unlink", "generic", "tool")
	file := filepath.Join(t.TempDir(), "tool-linux")
	require.NoError(t, os.WriteFile(file, []byte("binary"), 0o600))
	run("package", "upload", "tool", "2.0.0", file)
	require.Equal(t, map[string]string{"/api/packages/acme/generic/tool/2.0.0/tool-linux": "binary"}, uploaded)
	require.Contains(t, requests, "DELETE /api/v1/packages/acme/generic/tool")
	requireUsageBeforeRequests(t, "name the version to delete, or pass --all", "package", "delete", "generic", "tool")
}
