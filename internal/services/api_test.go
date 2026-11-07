// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/require"
)

func TestAPIPaginationRejectsUnsafeAndRepeatingLinks(t *testing.T) {
	foreignCalls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignCalls++ }))
	defer foreign.Close()
	for _, next := range []string{foreign.URL + "/api/v1/user?secret=hidden", "/api/v1/items", "/unrelated/items", "/api/v1/../user"} {
		t.Run(next, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "token secret", r.Header.Get("Authorization"))
				w.Header().Set("Link", "<"+next+">; rel=next")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer server.Close()
			err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "items", Method: "GET", Paginate: true}, func(response *http.Response) error { _, err := io.Copy(io.Discard, response.Body); return err })
			require.Error(t, err)
			require.NotContains(t, err.Error(), "hidden")
			require.Equal(t, 1, calls)
		})
	}
	require.Zero(t, foreignCalls)
	links, err := nextPageLinks([]string{`<https://example.org/items?a=1,2>; title="one,two"; rel="prev next", </last>; rel=last`})
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.org/items?a=1,2"}, links)
}

func TestAPIPaginationFollowsLinksNamingTheInstanceDifferently(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.Host+r.URL.RequestURI())
		if r.URL.Query().Get("page") != "2" {
			// Gitea builds Link headers from its ROOT_URL rather than from the request host.
			w.Header().Set("Link", `<https://git.example.test/api/v1/items?limit=1&page=2>; rel="next"`)
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	visit := func(response *http.Response) error { _, err := io.Copy(io.Discard, response.Body); return err }
	err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "items", Method: "GET", Paginate: true}, visit)
	require.NoError(t, err)
	require.Len(t, pages, 2)
	require.Contains(t, pages[1], "/api/v1/items?limit=1&page=2")
	require.Contains(t, pages[1], server.Listener.Addr().String(), "the second page still goes to the configured host")

	err = (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "other", Method: "GET", Paginate: true}, visit)
	require.ErrorContains(t, err, "git.example.test")
}

func TestAPIPaginationReadsAnEmptyListAnsweredWithNullAsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, total, body, want string
		paginate                bool
	}{
		// The timeline of an issue without events.
		{"null counted as empty", "0", "null\n", "[]", true},
		{"empty list", "0", "[]", "[]", true},
		{"null counted as more", "3", "null", "null", true},
		{"null not counted", "", "null", "null", true},
		{"without --paginate", "0", "null", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.total != "" {
					w.Header().Set("X-Total-Count", tc.total)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			var body []byte
			err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "repos/a/b/issues/1/timeline", Method: "GET", Paginate: tc.paginate, MaxPages: 1}, func(response *http.Response) error {
				var err error
				body, err = io.ReadAll(response.Body)
				return err
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, string(body))
		})
	}
}

func TestAPIVisitReturnsTheServerMessageOfAFailedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid color: #nothex"}`))
	}))
	defer server.Close()
	var body []byte
	err := (API{Client: transport.New(server.URL, "secret", false)}).Visit(context.Background(), APIOptions{Endpoint: "labels", Method: "POST"}, func(response *http.Response) error {
		var err error
		body, err = io.ReadAll(response.Body)
		return err
	})
	var apiErr *transport.APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "invalid color: #nothex", apiErr.Message)
	require.Contains(t, string(body), "nothex", "the visitor still sees the whole body")
}

func TestAPIMaxPagesDoesNotCountAProbePastTheEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		// total items of a timeline with ten items a page; null answers null past the end.
		total int
		null  bool
		// repeat answers every item to every page, as a list that ignores the page asked for.
		repeat bool
		pages  []int
		err    error
	}{
		{"empty probe", 20, false, false, []int{10, 10, 0}, nil},
		{"null probe", 20, true, false, []int{10, 10, 0}, nil},
		{"repeated probe", 10, false, true, []int{10, 10}, nil},
		{"more pages", 30, false, false, []int{10, 10, 10}, ErrMaxPages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/settings/api" {
					_, _ = w.Write([]byte(`{"max_response_items":50,"default_paging_num":30}`))
					return
				}
				page, _ := strconv.Atoi(cmp.Or(r.URL.Query().Get("page"), "1"))
				first, last := (page-1)*10+1, min(page*10, tc.total)
				if tc.repeat {
					first, last = 1, tc.total
				}
				items := []string{}
				for id := first; id <= last; id++ {
					items = append(items, strconv.Itoa(id))
				}
				pages = append(pages, len(items))
				list := "[" + strings.Join(items, ",") + "]"
				if tc.null && len(items) == 0 {
					list = "null"
				}
				// The timeline counts only the items of each page.
				w.Header().Set("X-Total-Count", strconv.Itoa(len(items)))
				_, _ = w.Write([]byte(list))
			}))
			defer server.Close()
			visited := 0
			visit := func(response *http.Response) error {
				visited++
				_, err := io.Copy(io.Discard, response.Body)
				return err
			}
			maxPages := len(tc.pages) - 1
			err := (API{Client: transport.New(server.URL, "", false)}).Visit(context.Background(), APIOptions{Endpoint: "repos/o/r/issues/1/timeline?limit=10", Method: "GET", Paginate: true, MaxPages: maxPages}, visit)
			require.Equal(t, tc.err, err)
			require.Equal(t, tc.pages, pages)
			require.Equal(t, maxPages, visited, "the page past the limit is not visited")
		})
	}
}

func TestAPIPaginationContinuesCountedListsWithoutLinks(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint string
		total          int
		// pageCount counts only the items of each page in X-Total-Count, as the timeline does.
		pageCount bool
		// ignoresPages answers every item to every page, as the comments of an issue do.
		ignoresPages bool
		// shape is how a page holds its items: "" a JSON array, "total_count" an object counting
		// the list in total_count, as the artifacts of a run do, and "object" an object counted in
		// X-Total-Count, as a topic search does.
		shape string
		// null answers null to a page past the end, as the timeline does.
		null bool
		// pages are the items of the pages asked for, printed those of the pages visited.
		pages, printed []int
	}{
		{"labels", "labels", 60, false, false, "", false, []int{30, 30}, []int{30, 30}},
		{"labels with limit", "labels?limit=100", 60, false, false, "", false, []int{50, 10}, []int{50, 10}},
		{"one short page", "labels", 12, false, false, "", false, []int{12}, []int{12}},
		{"one full page", "labels?limit=7", 7, false, false, "", false, []int{7}, []int{7}},
		{"timeline", "issues/1/timeline", 40, true, false, "", false, []int{30, 10}, []int{30, 10}},
		{"timeline with limit", "issues/1/timeline?limit=10", 20, true, false, "", false, []int{10, 10, 0}, []int{10, 10}},
		{"timeline answering null", "issues/1/timeline?limit=2", 4, true, false, "", true, []int{2, 2, 0}, []int{2, 2}},
		{"page ignored", "issues/1/comments", 31, false, true, "", false, []int{31}, []int{31}},
		{"page ignored over the maximum", "issues/1/comments", 56, false, true, "", false, []int{56}, []int{56}},
		{"timeline that ignores the page", "issues/1/timeline?limit=10", 10, true, true, "", false, []int{10, 10}, []int{10}},
		{"total_count", "actions/artifacts?limit=2", 5, false, false, "total_count", false, []int{2, 2, 1}, []int{2, 2, 1}},
		{"object counted in X-Total-Count", "topics?limit=1", 3, false, false, "object", false, []int{1, 1, 1}, []int{1, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/settings/api" {
					_, _ = w.Write([]byte(`{"max_response_items":50,"default_paging_num":30}`))
					return
				}
				size := 30
				if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
					size = min(limit, 50)
				}
				page, _ := strconv.Atoi(cmp.Or(r.URL.Query().Get("page"), "1"))
				first, last := (page-1)*size+1, min(page*size, tc.total)
				if tc.ignoresPages {
					first, last = 1, tc.total
				}
				items := []string{}
				for id := first; id <= last; id++ {
					items = append(items, strconv.Itoa(id))
				}
				pages = append(pages, len(items))
				list := "[" + strings.Join(items, ",") + "]"
				if tc.null && len(items) == 0 {
					list = "null"
				}
				switch tc.shape {
				case "total_count":
					_, _ = fmt.Fprintf(w, `{"total_count":%d,"artifacts":%s}`, tc.total, list)
					return
				case "object":
					list = `{"topics":` + list + `}`
				}
				count := tc.total
				if tc.pageCount {
					count = len(items)
				}
				w.Header().Set("X-Total-Count", strconv.Itoa(count))
				_, _ = w.Write([]byte(list))
			}))
			defer server.Close()
			var printed []int
			visit := func(response *http.Response) error {
				data, err := io.ReadAll(response.Body)
				var page any
				require.NoError(t, json.Unmarshal(data, &page))
				if object, ok := page.(map[string]any); ok {
					page = cmp.Or(object["topics"], object["artifacts"])
				}
				items, ok := page.([]any)
				require.True(t, ok, "printed page %s holds no list", data)
				printed = append(printed, len(items))
				return err
			}
			err := (API{Client: transport.New(server.URL, "", false)}).Visit(context.Background(), APIOptions{Endpoint: "repos/o/r/" + tc.endpoint, Method: "GET", Paginate: true}, visit)
			require.NoError(t, err)
			require.Equal(t, tc.pages, pages)
			require.Equal(t, tc.printed, printed)
		})
	}
}
