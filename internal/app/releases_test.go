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

func TestReleaseCreateWithTagFlagReturnsSingleJSONObject(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/releases", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","name":"v1"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "--tag", "v1", "-R", "alice/project", "--json=id,tag"}))
	require.JSONEq(t, `{"id":7,"tag":"v1"}`, out.String())
}

const firstRelease = `{"id":1,"tag_name":"v0.1.0","name":"First release","body":"First release.\n\n- login","target_commitish":"main",
	"html_url":"https://gitea.test/alice/project/releases/tag/v0.1.0","author":{"login":"alice"},"published_at":"2026-10-11T18:00:00Z",
	"assets":[{"id":3,"name":"notes.txt","size":13}]}`

func TestReleaseViewShowsTheLatestReleaseWithNotesAndAssets(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/releases/latest", r.URL.Path)
		_, _ = w.Write([]byte(firstRelease))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "dumb"})
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "view", "-R", "alice/project"}))
	require.Equal(t, `First release v0.1.0
Latest • alice released this about 2 hours ago · target main
Assets: notes.txt (13 B)

First release.

- login

View this release on Gitea: https://gitea.test/alice/project/releases/tag/v0.1.0
`, out.String())
}

func TestReleaseListShowsTypes(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":4,"tag_name":"v0.3.0","draft":true,"created_at":"2026-10-11T19:00:00Z"},
			{"id":3,"tag_name":"v0.2.0-rc1","name":"v0.2.0 RC1","prerelease":true,"published_at":"2026-10-11T18:00:00Z"},
			{"id":1,"tag_name":"v0.1.0","name":"First release","published_at":"2026-10-10T18:00:00Z"}]`))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "list", "-R", "alice/project"}))
	require.Equal(t, `Showing 3 of 3 releases in alice/project

TITLE          TYPE         TAG         PUBLISHED
v0.3.0         Draft        v0.3.0      about 1 hour ago
v0.2.0 RC1     Pre-release  v0.2.0-rc1  about 2 hours ago
First release  Latest       v0.1.0      about 1 day ago
`, out.String())
}

func TestReleaseDeleteSaysWhatHappenedToTheTag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":5,"tag_name":"v2.0.0"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "delete", "v2.0.0", "--yes", "-R", "alice/project"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Deleted release v2.0.0 from alice/project (tag kept; use --delete-tag)\n", diagnostics.String())
}

func TestReleaseCreateUploadsPositionalFiles(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/repos/alice/project/releases" {
			requirePayload(t, `{"tag_name":"v1.1.0","name":"v1.1.0","body":"Notes","draft":true}`, r.Body)
			_, _ = w.Write([]byte(`{"id":9,"tag_name":"v1.1.0","draft":true,"html_url":"https://gitea.test/alice/project/releases/tag/v1.1.0"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"gt.bin","size":2}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "gt.bin")
	require.NoError(t, os.WriteFile(file, []byte("hi"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "new", "v1.1.0", file, "-n", "Notes", "-d", "-R", "alice/project"}))
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/releases", "POST /api/v1/repos/alice/project/releases/9/assets"}, requests)
	require.Equal(t, "https://gitea.test/alice/project/releases/tag/v1.1.0\n", out.String())
	require.Equal(t, "✓ Created draft release v1.1.0 in alice/project\n✓ Uploaded gt.bin (2 B)\n", diagnostics.String())
}
