// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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
		case "/api/v1/repos/alice/project/issues/1":
			_, _ = w.Write([]byte(`{"number":1,"pull_request":{}}`))
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
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/1" {
				_, _ = w.Write([]byte(`{"number":1,"pull_request":{}}`))
				return
			}
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/1/assets/1" {
				_, _ = w.Write([]byte(`{"id":1,"name":"log.txt"}`))
				return
			}
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

func TestAttachmentTablesDownloadsAndDoneLines(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/1/assets":
			_, _ = w.Write([]byte(`[{"id":9,"name":"crash.log","size":2048,"download_count":3,"created_at":"2026-10-11T18:00:00Z","browser_download_url":"https://example.org/a/9"}]`))
		case "GET /api/v1/repos/alice/project/issues/1/assets/9":
			_, _ = w.Write([]byte(`{"id":9,"name":"crash.log","size":5,"uuid":"abc"}`))
		case "GET /attachments/abc":
			_, _ = w.Write([]byte("hello"))
		case "DELETE /api/v1/repos/alice/project/issues/1/assets/9":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	run := func(tty bool, args ...string) error {
		out.Reset()
		diagnostics.Reset()
		deps.Streams.OutTTY = tty
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
	}
	require.NoError(t, run(true, "issue", "attachment", "list", "1"))
	require.Equal(t, "Showing 1 of 1 attachments on alice/project#1\n\n"+
		"ID  NAME       SIZE     DOWNLOADS  CREATED\n"+
		"9   crash.log  2.0 KiB  3          about 2 hours ago\n", out.String())
	t.Chdir(t.TempDir())
	require.NoError(t, run(true, "issue", "attachment", "download", "1", "9"))
	require.Equal(t, "✓ Downloaded crash.log (5 B) to crash.log\n", diagnostics.String())
	data, err := os.ReadFile("crash.log")
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	require.ErrorContains(t, run(true, "issue", "attachment", "download", "1", "9"), "crash.log already exists; pass --clobber to replace it or --skip-existing to keep it")
	require.NoError(t, run(true, "issue", "attachment", "download", "1", "9", "--skip-existing"))
	require.Equal(t, "! Skipped attachment 9 (crash.log) on alice/project#1: crash.log exists\n", diagnostics.String())
	require.NoError(t, run(false, "issue", "attachment", "download", "1", "9", "-O", "-"))
	require.Equal(t, "hello", out.String())
	require.EqualError(t, run(true, "issue", "attachment", "delete", "1", "9"), "confirmation required to delete attachment 9 (crash.log) on alice/project#1; rerun with --yes")
	require.NoError(t, run(true, "issue", "attachment", "delete", "1", "9", "--yes"))
	require.Equal(t, "✓ Deleted attachment 9 (crash.log) on alice/project#1\n", diagnostics.String())
}

func TestAttachmentListViewAndDownloadOrdering(t *testing.T) {
	var fetched int
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/1/assets":
			_, _ = w.Write([]byte(`[{"id":9,"name":"crash.log","size":2048,"browser_download_url":"https://example.org/a/9"},{"id":10,"name":"b.log","size":1}]`))
		case "GET /api/v1/repos/alice/project/issues/1/assets/9":
			_, _ = w.Write([]byte(`{"id":9,"name":"crash.log","size":5,"uuid":"abc","browser_download_url":"https://example.org/a/9"}`))
		case "GET /attachments/abc":
			fetched++
			_, _ = w.Write([]byte("hello"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	run := func(tty bool, args ...string) error {
		out.Reset()
		deps.Streams.OutTTY = tty
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
	}
	// A limit that cuts the list says so.
	require.NoError(t, run(true, "issue", "attachment", "list", "1", "--limit", "1"))
	require.Equal(t, "Showing 1 of 2 attachments on alice/project#1 · next: --page 2\n", firstLine(out.String()))
	// Piped output keeps exact sizes and the download URL.
	require.NoError(t, run(false, "issue", "attachment", "list", "1", "--limit", "1"))
	require.Equal(t, "9\tcrash.log\t2048\t0\t\thttps://example.org/a/9\n", out.String())
	// An attachment has no description.
	require.NoError(t, run(true, "issue", "attachment", "view", "1", "9"))
	require.NotContains(t, out.String(), "No description provided")
	// Usage errors and skipped downloads never request the file.
	t.Chdir(t.TempDir())
	require.ErrorContains(t, run(true, "issue", "attachment", "download", "1", "9", "--clobber", "--skip-existing"), "cannot be combined")
	require.EqualError(t, run(false, "issue", "attachment", "download", "1", "9", "-O", "-", "--clobber"), "--clobber cannot be combined with --output -")
	require.EqualError(t, run(false, "issue", "attachment", "download", "1", "9", "-O", "-", "--skip-existing"), "--skip-existing cannot be combined with --output -")
	require.NoError(t, os.WriteFile("crash.log", []byte("old"), 0o600))
	require.NoError(t, run(true, "issue", "attachment", "download", "1", "9", "--skip-existing"))
	require.ErrorContains(t, run(true, "issue", "attachment", "download", "1", "9"), "already exists")
	require.Zero(t, fetched)
	require.NoError(t, run(true, "issue", "attachment", "download", "1", "9", "--clobber"))
	require.Equal(t, 1, fetched)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line + "\n"
}

func TestCommentAttachmentCommands(t *testing.T) {
	var mutations []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/comments/81":
			_, _ = w.Write([]byte(`{"id":81,"body":"x","issue_url":"https://example.org/alice/project/issues/1"}`))
		case "GET /api/v1/repos/alice/project/issues/comments/82":
			_, _ = w.Write([]byte(`{"id":82,"body":"x","pull_request_url":"https://example.org/alice/project/pulls/2"}`))
		case "GET /api/v1/repos/alice/project/issues/comments/81/assets":
			_, _ = w.Write([]byte(`[{"id":9,"name":"crash.log"},{"id":10,"name":"b.log"}]`))
		case "GET /api/v1/repos/alice/project/issues/comments/81/assets/9":
			_, _ = w.Write([]byte(`{"id":9,"name":"crash.log","size":5,"uuid":"abc"}`))
		case "GET /api/v1/repos/alice/project/issues/comments/81/assets/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"attachment not found"}`))
		case "GET /attachments/abc":
			_, _ = w.Write([]byte("hello"))
		case "PATCH /api/v1/repos/alice/project/issues/comments/81/assets/9":
			mutations = append(mutations, r.Method)
			_, _ = w.Write([]byte(`{"id":9,"name":"renamed.log"}`))
		case "DELETE /api/v1/repos/alice/project/issues/comments/81/assets/9":
			mutations = append(mutations, r.Method)
			w.WriteHeader(http.StatusNoContent)
		case "POST /api/v1/repos/alice/project/issues/comments/81/assets":
			mutations = append(mutations, r.Method)
			_, _ = w.Write([]byte(`{"id":11,"name":"new.log","size":3}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	run := func(args ...string) error {
		out.Reset()
		diagnostics.Reset()
		deps.Streams.OutTTY = !slices.Contains(args, "-")
		return Execute(context.Background(), deps, append(append([]string{"issue", "comment", "attachment"}, args...), "-R", "alice/project", "--no-input"))
	}
	require.NoError(t, run("list", "81", "--limit=1", "--page=2", "--json=id,name"))
	require.JSONEq(t, `[{"id":10,"name":"b.log"}]`, out.String())
	require.NoError(t, run("view", "81", "9", "--json=name"))
	require.JSONEq(t, `{"name":"crash.log"}`, out.String())
	require.EqualError(t, run("view", "81", "404"), "attachment 404 not found in comment 81 on alice/project")
	t.Chdir(t.TempDir())
	require.NoError(t, run("download", "81", "9", "-O", "-"))
	require.Equal(t, "hello", out.String())
	require.NoError(t, os.WriteFile("new.log", []byte("new"), 0o600))
	require.NoError(t, run("upload", "81", "new.log"))
	require.Equal(t, "✓ Uploaded new.log (3 B) to comment 81 on alice/project#1\n", diagnostics.String())
	require.NoError(t, run("edit", "81", "9", "--name", "renamed.log"))
	require.Equal(t, "✓ Renamed attachment 9 (renamed.log) on comment 81 on alice/project#1\n", diagnostics.String())
	require.EqualError(t, run("delete", "81", "9"), "confirmation required to delete attachment 9 (crash.log) on comment 81 on alice/project#1; rerun with --yes")
	require.NoError(t, run("delete", "81", "9", "--yes"))
	require.Equal(t, []string{http.MethodPost, http.MethodPatch, http.MethodDelete}, mutations)

	// pr commands refuse a comment on an issue before touching its attachments.
	err := Execute(context.Background(), deps, []string{"pr", "comment", "attachment", "delete", "81", "9", "-R", "alice/project", "--yes"})
	require.EqualError(t, err, "comment 81 in alice/project is on an issue, not a pull request; use gt issue comment attachment delete 81")
	require.Len(t, mutations, 3)
}
