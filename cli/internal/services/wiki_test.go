// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
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
	item, _, err := service.Edit(context.Background(), "Name with spaces", WikiOptions{Body: "ünicode\ntext"})
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
			items, err := (Wiki{Client: client, Repository: "alice/project"}).ListPage(context.Background(), 30, 1)
			if !wikiEnabled {
				require.EqualError(t, err, "the wiki of alice/project is disabled; turn it on with: gt repo edit --enable-wiki -R alice/project")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, items.Items)
			require.Empty(t, items.Items)
		})
	}
}

func TestWikiListAndRevisionsMapCommits(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/wiki/pages":
			assert.Equal(t, "50", r.URL.Query().Get("limit"))
			writeFirstPage(w, r, `[{"title":"Other"},{"title":"Home","html_url":"https://gitea.test/wiki/Home","last_commit":{"sha":"abc","author":{"name":"Alice","email":"author@example.test","date":"2026-10-04T00:00:00Z"}}},{"title":"Draft"}]`)
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
		{Title: "Other", listed: true},
		{Title: "Home", URL: "https://gitea.test/wiki/Home", SHA: "abc", Author: "Alice", UpdatedAt: "2026-10-04T00:00:00Z", listed: true},
	}, More: true, Total: 3, Counted: true}, pages)
	last, err := service.ListPage(context.Background(), 2, 2)
	require.NoError(t, err)
	require.Equal(t, []WikiPage{{Title: "Draft", listed: true}}, last.Items)
	// A listed page has no content to report, so its JSON leaves the content fields out.
	data, err := json.Marshal(pages.Items[1])
	require.NoError(t, err)
	require.JSONEq(t, `{"title":"Home","url":"https://gitea.test/wiki/Home","sha":"abc","author":"Alice","updated_at":"2026-10-04T00:00:00Z"}`, string(data))
	revisions, err := service.Revisions(context.Background(), "Home", 3)
	require.NoError(t, err)
	require.Equal(t, []WikiRevision{{SHA: "abc", Message: "Edit", Author: "Alice", CreatedAt: "2026-10-04T00:00:00Z"}, {SHA: "def"}}, revisions.Items)
	require.True(t, revisions.Counted)
	require.Equal(t, 2, revisions.Total)
}

func TestWikiListDoesNotPromiseAPageAfterAnEmptyWindow(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<`+r.URL.Path+`?page=2>; rel="next"`)
		}
		writeFirstPage(w, r, `[{"title":"One"},{"title":"Two"}]`)
	}))
	pages, err := (Wiki{Client: client, Repository: "alice/project"}).ListPage(context.Background(), 2, 1)
	require.NoError(t, err)
	require.Equal(t, Page[WikiPage]{Items: []WikiPage{{Title: "One", listed: true}, {Title: "Two", listed: true}}, Total: 2, Counted: true}, pages)
}

func TestWikiTrimsTitlesLikeGitea(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		title := map[string]string{"Home": "Home", "Draft": "Draft"}[path.Base(r.URL.Path)]
		if title == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	page, err := service.View(context.Background(), " Home ")
	require.NoError(t, err)
	require.Equal(t, "Home", page.Title)
	_, _, err = service.Edit(context.Background(), "Draft", WikiOptions{Title: "Home ", Body: "body"})
	var conflict *ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, `"Home"`, conflict.Key)
	const prefix = "GET /api/v1/repos/alice/project/wiki/page/"
	require.Equal(t, []string{prefix + "Home", prefix + "Draft", prefix + "Home"}, requests)
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
		_, _ = w.Write([]byte(`{"title":"New","content_base64":"Ym9keQ==","commit_count":1,"sidebar":"c2lkZQ==","footer":"Zm9vdA=="}`))
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	page, err := service.Create(context.Background(), WikiOptions{Title: "New", Body: "body", Message: "Add"})
	require.NoError(t, err)
	require.Equal(t, WikiPage{Title: "New", Body: "body", CommitCount: 1, Sidebar: "side", Footer: "foot"}, page)
	data, err := json.Marshal(page)
	require.NoError(t, err)
	require.JSONEq(t, `{"title":"New","body":"body","url":"","sha":"","author":"","updated_at":"","commit_count":1,"sidebar":"side","footer":"foot"}`, string(data))
	require.NoError(t, service.Delete(context.Background(), "Old page"))
}

func TestWikiAddressesPagesByTheirGiteaPath(t *testing.T) {
	var paths []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_wiki":true}`))
			return
		}
		// Gitea reads Getting-Started as the title Getting Started, but Getting-Started.- as the
		// title Getting-Started, which no page has.
		title := map[string]string{"A B-C.-": "A B-C", "notes.md.-": "notes.md", "Plain page": "Plain page", "Getting-Started": "Getting Started", "Getting Started": "Getting Started"}[path.Base(r.URL.Path)]
		switch {
		case title == "":
			http.NotFound(w, r)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/revisions/"):
			_, _ = w.Write([]byte(`{"commits":[],"count":0}`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
		}
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	ctx := context.Background()
	for _, name := range []string{"A B-C", "notes.md", "Plain page", "Getting-Started"} {
		_, err := service.View(ctx, name)
		require.NoError(t, err, name)
	}
	_, _, err := service.Edit(ctx, "Getting-Started", WikiOptions{Body: "body"})
	require.EqualError(t, err, `wiki page "Getting-Started" not found in alice/project; did you mean "Getting Started"?`)
	require.NoError(t, service.Delete(ctx, "A B-C"))
	_, err = service.Revisions(ctx, "A B-C", 1)
	require.NoError(t, err)
	const page = "/api/v1/repos/alice/project/wiki/page/"
	require.Equal(t, []string{
		"GET " + page + "A%20B-C.-", "GET " + page + "notes.md.-", "GET " + page + "Plain%20page",
		"GET " + page + "Getting-Started.-", "GET " + page + "Getting-Started",
		"GET " + page + "Getting-Started.-", "GET " + page + "Getting-Started", "GET /api/v1/repos/alice/project",
		"DELETE " + page + "A%20B-C.-",
		"GET /api/v1/repos/alice/project/wiki/revisions/A%20B-C.-",
	}, paths)
}

func TestWikiCreateExplainsADisabledWiki(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_wiki":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	_, err := (Wiki{Client: client, Repository: "alice/project"}).Create(context.Background(), WikiOptions{Title: "New", Body: "body"})
	require.ErrorContains(t, err, "the wiki of alice/project is disabled")
	require.NotErrorAs(t, err, new(*NotFoundError))
}

func TestWikiViewEditAndDeleteExplainADisabledWiki(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_wiki":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	_, viewErr := service.View(context.Background(), "Home")
	_, _, editErr := service.Edit(context.Background(), "Home", WikiOptions{Body: "body"})
	deleteErr := service.Delete(context.Background(), "Home")
	for _, err := range []error{viewErr, editErr, deleteErr} {
		require.ErrorContains(t, err, "the wiki of alice/project is disabled")
		require.ErrorContains(t, err, "gt repo edit --enable-wiki")
		require.NotErrorAs(t, err, new(*NotFoundError))
	}
}

func TestWikiDeleteNamesAMissingPageOfAWikiNeverCreated(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed to clone repository: alice/project (exit status 128 - fatal: Remote branch main not found in upstream origin)"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"no such file or directory"}`))
	}))
	err := (Wiki{Client: client, Repository: "alice/project"}).Delete(context.Background(), "Home")
	require.EqualError(t, err, `wiki page "Home" not found in alice/project`)
}

func TestWikiChangesReachOnlyThePageTheyName(t *testing.T) {
	pages := map[string]bool{"Foo-Bar": true, "Foo Bar": true}
	var changed []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_wiki":true}`))
			return
		}
		// Gitea reads a path without the ".-" mark as a title with its spaces written as "-".
		title, marked := strings.CutSuffix(path.Base(r.URL.Path), ".-")
		if !marked {
			title = strings.ReplaceAll(title, "-", " ")
		}
		switch {
		case !pages[title]:
			http.NotFound(w, r)
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
		default:
			changed = append(changed, r.Method+" "+title)
			if r.Method == http.MethodDelete {
				delete(pages, title)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
		}
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	ctx := context.Background()
	page, err := service.Find(ctx, "Foo-Bar")
	require.NoError(t, err)
	require.NoError(t, service.Delete(ctx, page.Title))
	const missing = `wiki page "Foo-Bar" not found in alice/project; did you mean "Foo Bar"?`
	_, err = service.Find(ctx, "Foo-Bar")
	require.EqualError(t, err, missing)
	require.True(t, IsNotFound(err))
	_, _, err = service.Edit(ctx, "Foo-Bar", WikiOptions{Body: "body"})
	require.EqualError(t, err, missing)
	require.Error(t, service.Delete(ctx, "Foo-Bar"))
	require.Equal(t, []string{"DELETE Foo-Bar"}, changed)
	require.True(t, pages["Foo Bar"])
	// Reading still follows the page path of a page URL.
	page, err = service.View(ctx, "Foo-Bar")
	require.NoError(t, err)
	require.Equal(t, "Foo Bar", page.Title)
}

func TestWikiAddressesTitlesWithReservedCharacters(t *testing.T) {
	titles := []string{"C++", "a+b c", "Plain page", "A B-C", "dash-ed", "a/b", "ünïcode", "notes.md", "100%", "what?", "#1", `back\slash`}
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Gitea reads the page path like a query value, so a "+" in it is a space.
		raw, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/v1/repos/alice/project/wiki/page/")
		if !ok {
			raw, ok = strings.CutPrefix(r.URL.EscapedPath(), "/api/v1/repos/alice/project/wiki/revisions/")
		}
		if !assert.True(t, ok, r.URL.EscapedPath()) {
			return
		}
		if trimmed, marked := strings.CutSuffix(raw, ".-"); marked {
			raw = trimmed
		} else {
			raw = strings.ReplaceAll(raw, "-", " ")
		}
		title, err := url.QueryUnescape(raw)
		assert.NoError(t, err)
		requests = append(requests, r.Method+" "+title)
		if !slices.Contains(titles, title) {
			http.NotFound(w, r)
			return
		}
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/revisions/"):
			_, _ = w.Write([]byte(`{"commits":[{"sha":"abc"}],"count":1}`))
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"title": title})
		}
	}))
	service := Wiki{Client: client, Repository: "alice/project"}
	ctx := context.Background()
	for _, title := range titles {
		requests = nil
		page, err := service.View(ctx, title)
		require.NoError(t, err, title)
		require.Equal(t, title, page.Title)
		_, _, err = service.Edit(ctx, title, WikiOptions{Body: "body"})
		require.NoError(t, err, title)
		revisions, err := service.Revisions(ctx, title, 1)
		require.NoError(t, err, title)
		require.Len(t, revisions.Items, 1)
		require.NoError(t, service.Delete(ctx, title), title)
		require.Equal(t, []string{"GET " + title, "GET " + title, "PATCH " + title, "GET " + title, "DELETE " + title}, requests)
	}
}

func TestWikiRevisionsNameTheMissingPage(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_wiki":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"no such file or directory"}`))
	}))
	_, err := (Wiki{Client: client, Repository: "alice/project"}).Revisions(context.Background(), "nosuch", 1)
	require.EqualError(t, err, `wiki page "nosuch" not found in alice/project`)
}
