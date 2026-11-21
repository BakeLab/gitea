// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryOwnerLookupOnlyFallsBackOnNotFound(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := []string{}
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.URL.Path == "/api/v1/orgs/owner" {
					w.WriteHeader(status)
					return
				}
				if r.URL.Path == "/api/v1/users/owner" {
					_, _ = w.Write([]byte(`{"id":2}`))
					return
				}
				_, _ = w.Write([]byte(`[]`))
			}))
			items, _, err := (Repositories{Client: client}).List(context.Background(), RepositoryListOptions{Owner: "owner", Limit: 30, Page: 1})
			if status == http.StatusForbidden {
				require.Error(t, err)
				require.Equal(t, []string{"/api/v1/orgs/owner"}, calls)
			} else {
				require.NoError(t, err)
				require.NotNil(t, items)
				require.Equal(t, []string{"/api/v1/orgs/owner", "/api/v1/users/owner", "/api/v1/users/owner/repos"}, calls)
			}
		})
	}
}

func TestRepositoryListSelectsCollection(t *testing.T) {
	for name, test := range map[string]struct {
		opts RepositoryListOptions
		path string
	}{
		"account":      {RepositoryListOptions{}, "/api/v1/user/repos"},
		"user":         {RepositoryListOptions{Owner: "bob"}, "/api/v1/users/bob/repos"},
		"organization": {RepositoryListOptions{Owner: "acme"}, "/api/v1/orgs/acme/repos"},
		"starred":      {RepositoryListOptions{Starred: true}, "/api/v1/user/starred"},
		"watched":      {RepositoryListOptions{Watched: true}, "/api/v1/user/subscriptions"},
		"owner first":  {RepositoryListOptions{Owner: "bob", Starred: true}, "/api/v1/users/bob/repos"},
	} {
		t.Run(name, func(t *testing.T) {
			var listed []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/orgs/acme":
					_, _ = w.Write([]byte(`{"id":3,"username":"acme"}`))
				case "/api/v1/orgs/bob":
					w.WriteHeader(http.StatusNotFound)
				case "/api/v1/users/bob":
					_, _ = w.Write([]byte(`{"id":2,"login":"bob"}`))
				default:
					listed = append(listed, r.URL.Path)
					assert.Equal(t, "2", r.URL.Query().Get("page"))
					assert.Equal(t, "1", r.URL.Query().Get("limit"))
					w.Header().Set("X-Total-Count", "3")
					_, _ = w.Write([]byte(`[{"id":2,"name":"second","fork":true}]`))
				}
			}))
			test.opts.Limit, test.opts.Page = 1, 2
			items, more, err := (Repositories{Client: client}).List(context.Background(), test.opts)
			require.NoError(t, err)
			require.Equal(t, []string{test.path}, listed)
			require.Equal(t, []Repository{{ID: 2, Name: "second", Fork: true}}, items)
			require.True(t, more)
		})
	}
}

func TestRepositoryListFiltersKind(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"fork":true},{"id":2,"mirror":true},{"id":3}]`))
	}))
	for kind, want := range map[string]int64{"fork": 1, "mirror": 2, "source": 3} {
		items, _, err := (Repositories{Client: client}).List(context.Background(), RepositoryListOptions{Kind: kind, Limit: 30, Page: 1})
		require.NoError(t, err)
		require.Len(t, items, 1, kind)
		require.Equal(t, want, items[0].ID, kind)
	}
}

func TestRepositoryListKindFillsThePageFromLaterServerPages(t *testing.T) {
	// The account has 60 repositories, of which every third is a fork.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var items []string
		for id := (page-1)*limit + 1; id <= min(page*limit, 60); id++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"fork":%t}`, id, id%3 == 0))
		}
		w.Header().Set("X-Total-Count", "60")
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	page, err := (Repositories{Client: client}).ListPage(context.Background(), RepositoryListOptions{Kind: "fork", Limit: 5, Page: 2})
	require.NoError(t, err)
	ids := make([]int64, len(page.Items))
	for i, item := range page.Items {
		ids[i] = item.ID
	}
	require.Equal(t, []int64{18, 21, 24, 27, 30}, ids)
	require.True(t, page.More)
	page, err = (Repositories{Client: client}).ListPage(context.Background(), RepositoryListOptions{Kind: "fork", Limit: 5, Page: 4})
	require.NoError(t, err)
	require.Len(t, page.Items, 5)
	require.False(t, page.More)
	require.Equal(t, 20, page.Total)
}

func TestRepositorySearchSendsExplicitFalseFilters(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"id":7}`))
			return
		}
		assert.Equal(t, "/api/v1/repos/search", r.URL.Path)
		query := r.URL.Query()
		assert.Equal(t, "false", query.Get("is_private"))
		assert.Equal(t, "false", query.Get("archived"))
		assert.Equal(t, "true", query.Get("topic"))
		assert.Equal(t, "7", query.Get("priority_owner_id"))
		assert.Equal(t, "two words", query.Get("q"))
		assert.Equal(t, "true", query.Get("includeDesc"))
		assert.Equal(t, "30", query.Get("limit"))
		assert.Equal(t, "1", query.Get("page"))
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	value := false
	page, err := (Repositories{Client: client}).Search(context.Background(), RepositorySearchOptions{Query: "two words", Private: &value, Archived: &value, Topic: true, Limit: 30, Page: 1})
	require.NoError(t, err)
	require.NotNil(t, page.Items)
	require.Empty(t, page.Items)
}

func TestRepositorySearchSortDropsOwnerPriority(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"id":7}`))
			return
		}
		query := r.URL.Query()
		assert.False(t, query.Has("priority_owner_id"))
		assert.Equal(t, "updated", query.Get("sort"))
		assert.Equal(t, "desc", query.Get("order"))
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	_, err := (Repositories{Client: client}).Search(context.Background(), RepositorySearchOptions{Query: "go", Sort: "updated", Order: "desc", Limit: 30, Page: 1})
	require.NoError(t, err)
}

func TestRepositorySearchScopesOwner(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":3,"username":"acme"}`))
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"id":7,"login":"alice"}`))
		default:
			query := r.URL.Query()
			assert.Equal(t, "3", query.Get("uid"))
			assert.Equal(t, "true", query.Get("exclusive"))
			assert.Equal(t, "fork", query.Get("mode"))
			assert.Equal(t, "updated", query.Get("sort"))
			assert.Equal(t, "desc", query.Get("order"))
			assert.False(t, query.Has("is_private"))
			_, _ = w.Write([]byte(`{"data":[{"id":4,"full_name":"acme/tool"}]}`))
		}
	}))
	page, err := (Repositories{Client: client}).Search(context.Background(), RepositorySearchOptions{Owner: "acme", Kind: "fork", Sort: "updated", Order: "desc", Limit: 30, Page: 1})
	require.NoError(t, err)
	require.Equal(t, []Repository{{ID: 4, FullName: "acme/tool"}}, page.Items)
}
