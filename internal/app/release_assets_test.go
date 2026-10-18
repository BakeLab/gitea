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

	"gitea.dev/cli/internal/clierr"
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
	err = Execute(context.Background(), deps, []string{"release", "upload", "v1", first, second, "-R", "owner/repo", "--json"})
	require.ErrorContains(t, err, "first.txt")
	require.Equal(t, 2, uploads)
	require.JSONEq(t, `[{"id":2,"name":"second.txt","size":0,"url":"","created_at":"","download_count":0}]`, out.String())
}

func TestReleaseUploadRefusesDuplicateNamesWithoutClobber(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":7,"name":"hello.txt"}]}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"id":8,"name":"hello.txt","size":5}`))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "hello.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	err := Execute(context.Background(), deps, []string{"release", "asset", "upload", "v1", file, "-R", "owner/repo"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "asset hello.txt already exists in release v1; pass --clobber to replace it")
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1"}, requests)
	requests = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "upload", "v1", file, "--clobber", "-R", "owner/repo"}))
	require.Equal(t, []string{"GET /api/v1/repos/owner/repo/releases/tags/v1", "POST /api/v1/repos/owner/repo/releases/1/assets", "DELETE /api/v1/repos/owner/repo/releases/1/assets/7"}, requests)
	require.Equal(t, "✓ Uploaded hello.txt (5 B) to release v1\n", diagnostics.String())
}

func TestReleaseAssetNameSelectionValidatedBeforeDeletion(t *testing.T) {
	var deleted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"one.txt"},{"id":2,"name":"2"}]}`))
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "missing", "--yes", "-R", "owner/repo"})
	require.EqualError(t, err, `asset "missing" not found in release v1`)
	require.Empty(t, deleted)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "asset", "delete", "v1", "one.txt", "2", "--by-name", "--yes", "-R", "owner/repo"}))
	require.Equal(t, []string{"/api/v1/repos/owner/repo/releases/1/assets/1", "/api/v1/repos/owner/repo/releases/1/assets/2"}, deleted)
	require.Equal(t, "✓ Deleted asset one.txt (ID 1) from release v1\n✓ Deleted asset 2 (ID 2) from release v1\n", diagnostics.String())
}

func TestReleaseDownloadSelectsAssetsOfTheLatestRelease(t *testing.T) {
	var downloads []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/owner/repo/releases/latest", r.URL.Path == "/api/v1/repos/owner/repo/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":1,"tag_name":"v1","assets":[{"id":1,"name":"gt-linux","size":5,"uuid":"aaa"},{"id":2,"name":"gt-darwin","size":5,"uuid":"bbb"},{"id":3,"name":"notes.txt","size":5,"uuid":"ccc"}]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/releases/1/assets/")
			uuid := map[string]string{"1": "aaa", "2": "bbb", "3": "ccc"}[id]
			_, _ = w.Write([]byte(`{"id":` + id + `,"uuid":"` + uuid + `"}`))
		case strings.HasPrefix(r.URL.Path, "/attachments/"):
			downloads = append(downloads, r.URL.Path)
			_, _ = w.Write([]byte("bytes"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := repoTTYDeps(t, server.URL)
	dir := t.TempDir()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "download", "--pattern", "gt-*", "--dir", dir, "-R", "owner/repo"}))
	require.Equal(t, []string{"/attachments/aaa", "/attachments/bbb"}, downloads)
	content, err := os.ReadFile(filepath.Join(dir, "gt-darwin"))
	require.NoError(t, err)
	require.Equal(t, "bytes", string(content))
	require.Equal(t, "✓ Downloaded gt-linux (5 B) to "+filepath.Join(dir, "gt-linux")+"\n✓ Downloaded gt-darwin (5 B) to "+filepath.Join(dir, "gt-darwin")+"\n", diagnostics.String())
	err = Execute(context.Background(), deps, []string{"release", "asset", "download", "v1", "--pattern", "*.exe", "--dir", dir, "-R", "owner/repo"})
	require.Error(t, err)
	err = Execute(context.Background(), deps, []string{"release", "download", "v1", "notes.txt", "gt-linux", "-O", filepath.Join(dir, "one"), "-R", "owner/repo"})
	require.ErrorContains(t, err, "--output names one file, but 2 assets match; pass --dir")
}
