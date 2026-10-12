// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikiContentAndNamedPagePath(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/wiki/page/Name with spaces", r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"title":"Name with spaces"}`))
			return
		}
		assert.Equal(t, http.MethodPatch, r.Method)
		var body map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("ünicode\ntext")), body["content_base64"])
		assert.Equal(t, "Name with spaces", body["title"])
		_, _ = w.Write([]byte(`{"title":"Name with spaces","content_base64":"w7xuaWNvZGUKdGV4dA==","last_commit":{"sha":"abc","author":{"name":"Alice","email":"author@example.test","date":"2026-10-04T00:00:00Z"}}}`))
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	item, err := service.Edit(context.Background(), "Name with spaces", WikiOptions{Body: "ünicode\ntext"})
	require.NoError(t, err)
	require.Equal(t, "ünicode\ntext", item.Body)
	require.Equal(t, "Alice", item.Author)
	data, err := json.Marshal(item)
	require.NoError(t, err)
	require.NotContains(t, string(data), "author@example.test")
}

func TestWikiMalformedContentAndCancellation(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"content_base64":"invalid!"}`)) }))
	service := Wiki{Client: client, Repository: "alice/project"}
	_, err := service.View(context.Background(), "Home")
	require.ErrorContains(t, err, "decode wiki content")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.View(ctx, "Home")
	require.ErrorIs(t, err, context.Canceled)
}

func TestWikiEmptyListDoesNotHideRepositoryErrors(t *testing.T) {
	for name, wikiEnabled := range map[string]bool{"enabled": true, "disabled": false} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/repos/alice/project" {
					_ = json.NewEncoder(w).Encode(map[string]bool{"has_wiki": wikiEnabled})
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			items, err := (Wiki{Client: client, Repository: "alice/project"}).List(context.Background(), 30, 1)
			if !wikiEnabled {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, items)
			require.Empty(t, items)
		})
	}
}

func TestWikiListAndRevisionsMapCommits(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/wiki/pages":
			assert.Equal(t, "50", r.URL.Query().Get("limit"))
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"title":"Other"},{"title":"Home","html_url":"https://gitea.test/wiki/Home","last_commit":{"sha":"abc","author":{"name":"Alice","email":"author@example.test","date":"2026-10-04T00:00:00Z"}}},{"title":"Draft"}]`))
		case "/api/v1/repos/alice/project/wiki/revisions/Home":
			assert.Equal(t, "3", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`{"commits":[{"sha":"abc","message":"Edit","author":{"name":"Alice","email":"author@example.test","date":"2026-10-04T00:00:00Z"}},{"sha":"def","author":null}],"count":2}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	pages, err := service.ListPage(context.Background(), 2, 1)
	require.NoError(t, err)
	require.Equal(t, Page[WikiPage]{Items: []WikiPage{
		{Title: "Other"},
		{Title: "Home", URL: "https://gitea.test/wiki/Home", SHA: "abc", Author: "Alice", UpdatedAt: "2026-10-04T00:00:00Z"},
	}, More: true, Total: 3, Counted: true}, pages)
	last, err := service.List(context.Background(), 2, 2)
	require.NoError(t, err)
	require.Equal(t, []WikiPage{{Title: "Draft"}}, last)
	revisions, err := service.Revisions(context.Background(), "Home", 3)
	require.NoError(t, err)
	require.Equal(t, []WikiRevision{{SHA: "abc", Message: "Edit", Author: "Alice", CreatedAt: "2026-10-04T00:00:00Z"}, {SHA: "def"}}, revisions)
}

func TestWikiCreateAndDelete(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			assert.Equal(t, "/api/v1/repos/alice/project/wiki/page/Old%20page", r.URL.EscapedPath())
			w.WriteHeader(http.StatusNoContent)
			return
		}
		assert.Equal(t, "/api/v1/repos/alice/project/wiki/new", r.URL.Path)
		var body map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]string{"title": "New", "content_base64": "Ym9keQ==", "message": "Add"}, body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"title":"New","content_base64":"Ym9keQ==","commit_count":1,"sidebar":"side","footer":"foot"}`))
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	page, err := service.Create(context.Background(), WikiOptions{Title: "New", Body: "body", Message: "Add"})
	require.NoError(t, err)
	require.Equal(t, WikiPage{Title: "New", Body: "body", CommitCount: 1, Sidebar: "side", Footer: "foot"}, page)
	require.NoError(t, service.Delete(context.Background(), "Old page"))
}
