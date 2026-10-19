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

func TestLabelExportReadsAllPagesAndPublishesOnlyCompleteFiles(t *testing.T) {
	fail := false
	pages := []string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			_, _ = w.Write([]byte(`[{"name":"good first issue","color":"ff0000","description":"Help wanted"}]`))
		case "2":
			if fail {
				w.WriteHeader(403)
				return
			}
			_, _ = w.Write([]byte(`[{"name":"bug","color":"00ff00","description":""}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	output := filepath.Join(t.TempDir(), "labels.txt")
	args := []string{"label", "export", "-R", "alice/project", "--output", output}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Empty(t, out.String())
	require.Equal(t, []string{"1", "2", "3"}, pages)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "#ff0000 good first issue ;Help wanted\n#00ff00 bug\n", string(data))
	require.NoError(t, os.Remove(output))
	fail = true
	require.Error(t, Execute(context.Background(), deps, args))
	_, err = os.Stat(output)
	require.True(t, os.IsNotExist(err))
}

func TestLabelJSONExportPreservesTextAndTextFailureLeavesNoFile(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"name":"first","color":"ff0000","description":""}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"name":"two  spaces;name","color":"00ff00","description":"line\nbreak"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	directory := t.TempDir()
	output := filepath.Join(directory, "labels.txt")
	args := []string{"label", "export", "-R", "alice/project", "--output", output}
	require.ErrorContains(t, Execute(context.Background(), deps, args), "label export --json")
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Empty(t, out.String())
	require.NoError(t, Execute(context.Background(), deps, append(args, "--json=id,name,description")))
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":1,"name":"first","description":""},{"id":2,"name":"two  spaces;name","description":"line\nbreak"}]`, string(data))
	require.Empty(t, out.String())
}

func TestEmptyLabelJSONExportIsAnArray(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "export", "-R", "alice/project", "--output", "-", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestLabelExportStripsEscapesOnATerminalOnly(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`[{"name":"bug\u001b[8m","color":"ff0000","description":""}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"label", "export", "-R", "alice/project"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Contains(t, out.String(), "\x1b[8m")
	out.Reset()
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, args))
	require.NotContains(t, out.String(), "\x1b")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "-O", "-", "--json", "--jq", ".[0].name")))
	require.NotContains(t, out.String(), "\x1b")
}
