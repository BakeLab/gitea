// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

// cappedServer serves total items in pages of at most maxItems, like Gitea's MAX_RESPONSE_ITEMS.
func cappedServer(t *testing.T, total, maxItems int, link, count bool) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		require.NoError(t, err)
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		require.NoError(t, err)
		size := min(limit, maxItems)
		items := []string{}
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"name":"n%d","full_name":"o/n%d"}`, id, id, id))
		}
		if count {
			w.Header().Set("X-Total-Count", strconv.Itoa(total))
		}
		if link && page*size < total {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?limit=%d&page=%d>; rel="next"`, "http://"+r.Host, r.URL.Path, limit, page+1))
		}
		_, _ = w.Write([]byte("[" + strings.Join(items, ",") + "]"))
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestListsCollectPagesBeyondServerPageSize(t *testing.T) {
	for _, tc := range []struct {
		total, maxItems, limit, page int
		link, count                  bool
	}{
		{120, 50, 100, 1, true, true},
		{120, 50, 100, 2, true, true},
		{120, 50, 60, 2, true, true},
		{120, 50, 30, 3, true, true},
		{120, 50, 100, 3, true, true},
		{100, 50, 100, 2, true, true},
		{110, 60, 100, 2, true, true},
		{120, 50, 100, 1, true, false},
		{120, 50, 100, 2, true, false},
		{120, 50, 100, 1, false, true},
		{120, 50, 100, 2, false, true},
		{110, 60, 100, 2, false, true},
		{40, 50, 100, 1, false, false},
		{120, 100, 100, 2, true, true},
	} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			server, requests := cappedServer(t, tc.total, tc.maxItems, tc.link, tc.count)
			want := []int64{}
			for id := (tc.page-1)*tc.limit + 1; id <= min(tc.page*tc.limit, tc.total); id++ {
				want = append(want, int64(id))
			}
			client := transport.New(server.URL, "", false)
			orgs, more, err := (Organizations{Client: client}).List(context.Background(), true, "", tc.limit, tc.page)
			require.NoError(t, err)
			got := []int64{}
			for _, item := range orgs {
				got = append(got, item.ID)
			}
			require.Equal(t, want, got)
			require.Equal(t, tc.total > tc.page*tc.limit, more)
			require.LessOrEqual(t, *requests, tc.limit/min(tc.limit, tc.maxItems)+2)
			repos, more, err := (Repositories{Client: client}).List(context.Background(), RepositoryListOptions{Limit: tc.limit, Page: tc.page})
			require.NoError(t, err)
			got = []int64{}
			for _, item := range repos {
				got = append(got, item.ID)
			}
			require.Equal(t, want, got)
			require.Equal(t, tc.total > tc.page*tc.limit, more)
		})
	}
}

func TestPagesReportTheCountedTotal(t *testing.T) {
	for _, tc := range []struct {
		total, maxItems, limit, page int
		count, more                  bool
	}{
		{120, 50, 30, 1, true, true},
		{120, 50, 1000, 1, true, false},
		{120, 50, 100, 2, true, false},
		{40, 50, 100, 1, false, false},
	} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			server, _ := cappedServer(t, tc.total, tc.maxItems, true, tc.count)
			client := transport.New(server.URL, "", false)
			query := url.Values{"state": {"open"}}
			window, err := rawPage[Organization](context.Background(), client, "orgs", query, tc.limit, tc.page)
			require.NoError(t, err)
			require.Len(t, window.Items, min(tc.limit, max(0, tc.total-(tc.page-1)*tc.limit)))
			require.Equal(t, tc.more, window.More)
			require.Equal(t, tc.count, window.Counted)
			if tc.count {
				require.Equal(t, tc.total, window.Total)
			}
			require.Equal(t, url.Values{"state": {"open"}}, query, "the caller's query is not modified")
		})
	}
}
