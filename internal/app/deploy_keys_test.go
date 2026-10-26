// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeployKeysListAddAndDelete(t *testing.T) {
	key, _ := testPublicKey(t, "ci@build")
	file := filepath.Join(t.TempDir(), "ci.pub")
	// Gitea takes neither the comment lines nor the options of an authorized_keys file.
	require.NoError(t, os.WriteFile(file, []byte("# build server\nno-pty "+key+"\n"), 0o600))
	var requests []string
	var created map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/keys":
			writeFirstPage(w, r, `[{"id":4,"title":"ci","fingerprint":"SHA256:abc","read_only":true}]`)
		case "POST /api/v1/repos/alice/project/keys":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&created))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":5,"title":"release","fingerprint":"SHA256:def","read_only":false}`))
		case "GET /api/v1/repos/alice/project/keys/4":
			_, _ = w.Write([]byte(`{"id":4,"title":"ci","fingerprint":"SHA256:abc","read_only":true}`))
		case "DELETE /api/v1/repos/alice/project/keys/4":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "deploy-key", "list", "-R", "alice/project"}))
	require.Contains(t, out.String(), "4\tci\tread-only\tSHA256:abc")
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "deploy-key", "add", file, "--title", "release", "--allow-write", "-R", "alice/project"}))
	require.Equal(t, false, created["read_only"])
	require.Equal(t, "release", created["title"])
	require.Equal(t, key, created["key"])
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "deploy-key", "delete", "4", "--yes", "-R", "alice/project"}))
	require.Contains(t, requests, "DELETE /api/v1/repos/alice/project/keys/4")
	untitled := filepath.Join(t.TempDir(), "untitled.pub")
	require.NoError(t, os.WriteFile(untitled, []byte(strings.Fields(key)[0]+" "+strings.Fields(key)[1]+"\n"), 0o600))
	requireUsageBeforeRequests(t, "key title cannot be empty; pass --title", "repo", "deploy-key", "add", untitled)
}
