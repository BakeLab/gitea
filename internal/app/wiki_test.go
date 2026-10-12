// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWikiEditRenamesAndKeepsTheContent(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			requirePayload(t, `{"title":"New Title","content_base64":"S2VlcCBtZQ=="}`, r.Body)
			_, _ = w.Write([]byte(`{"title":"New Title","content_base64":"S2VlcCBtZQ=="}`))
			return
		}
		_, _ = w.Write([]byte(`{"title":"Old Title","content_base64":"S2VlcCBtZQ=="}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "edit", "Old Title", "--title", "New Title", "-R", "alice/x", "--no-input"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/x/wiki/page/Old Title", "PATCH /api/v1/repos/alice/x/wiki/page/Old Title"}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Renamed wiki page \"Old Title\" to \"New Title\" in alice/x\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"wiki", "edit", "Home", "-R", "alice/x"})
	require.ErrorContains(t, err, "supply --title, --body, --body-file or --editor")
}

func TestWikiListLimitsPagesOnTheClient(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"title":"Getting Started","last_commit":{"author":{"name":"alice","date":"2026-10-11T18:00:00Z"}}},{"title":"Home"}]`))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "list", "--limit", "1", "-R", "alice/x"}))
	require.Equal(t, `Showing 1 of 2 wiki pages in alice/x · next: --page 2

TITLE            AUTHOR  UPDATED
Getting Started  alice   about 2 hours ago
`, out.String())
}

func TestWikiViewAndCreate(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			requirePayload(t, `{"title":"Page Two","content_base64":"Ym9keQ=="}`, r.Body)
		}
		_, _ = w.Write([]byte(`{"title":"Page Two","html_url":"https://gitea.test/alice/x/wiki/Page-Two","content_base64":"Ym9keQ==","commit_count":3,
			"last_commit":{"author":{"name":"alice","date":"2026-10-11T19:00:00Z"}}}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "dumb"})
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "view", "Page Two", "-R", "alice/x"}))
	require.Equal(t, "Page Two\nalice · updated about 1 hour ago · 3 revisions\n\nbody\n\nView this wiki page on Gitea: https://gitea.test/alice/x/wiki/Page-Two\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"wiki", "create", "Page Two", "-b", "body", "-R", "alice/x"}))
	require.Equal(t, "https://gitea.test/alice/x/wiki/Page-Two\n", out.String())
	require.Equal(t, "✓ Created wiki page \"Page Two\" in alice/x\n", diagnostics.String())
}
