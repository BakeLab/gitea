// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/require"
)

// cappedServer serves total items in pages of at most maxItems, like Gitea's MAX_RESPONSE_ITEMS.
func cappedServer(t *testing.T, total, maxItems int, link, count bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	return wrappedCappedServer(t, total, maxItems, link, count, func(items string, _ int) string { return items })
}

// wrappedCappedServer is cappedServer for endpoints that wrap the JSON array of a page in an object.
func wrappedCappedServer(t *testing.T, total, maxItems int, link, count bool, wrap func(items string, total int) string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/api/v1/settings/api" {
			_, _ = fmt.Fprintf(w, `{"max_response_items":%d}`, maxItems)
			return
		}
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
		_, _ = w.Write([]byte(wrap("["+strings.Join(items, ",")+"]", total)))
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
		// The capped page 2 of 50 items accounts for the remainder of 120 after the first 70.
		{120, 50, 70, 2, false, true},
		{120, 50, 70, 2, true, true},
		{100, 50, 70, 2, false, true},
	} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			server, requests := cappedServer(t, tc.total, tc.maxItems, tc.link, tc.count)
			want := []int64{}
			for id := (tc.page-1)*tc.limit + 1; id <= min(tc.page*tc.limit, tc.total); id++ {
				want = append(want, int64(id))
			}
			client := transport.New(server.URL, "", false)
			orgs, err := (Organizations{Client: client}).List(context.Background(), true, "", tc.limit, tc.page)
			require.NoError(t, err)
			got := []int64{}
			for _, item := range orgs.Items {
				got = append(got, item.ID)
			}
			require.Equal(t, want, got)
			require.Equal(t, tc.total > tc.page*tc.limit, orgs.More)
			require.LessOrEqual(t, int(requests.Load()), tc.limit/min(tc.limit, tc.maxItems)+2)
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
			window, err := callPage(context.Background(), client, tc.limit, tc.page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Organization, *gitea.Response, error) {
				return c.Organizations.ListOrgs(ctx, gitea.ListOrgsOptions{ListOptions: list})
			})
			require.NoError(t, err)
			require.Len(t, window.Items, min(tc.limit, max(0, tc.total-(tc.page-1)*tc.limit)))
			require.Equal(t, tc.more, window.More)
			require.Equal(t, tc.count, window.Counted)
			if tc.count {
				require.Equal(t, tc.total, window.Total)
			}
		})
	}
}

func TestFullListingsStopAtTheCountedEnd(t *testing.T) {
	for _, tc := range []struct {
		total, maxItems int
		count           bool
		requests        int
	}{
		{40, 50, true, 1},
		{100, 100, true, 1},
		{120, 50, true, 4},
		// Without a count only an empty page ends the listing.
		{40, 50, false, 2},
	} {
		t.Run(fmt.Sprintf("%+v", tc), func(t *testing.T) {
			server, requests := cappedServer(t, tc.total, tc.maxItems, false, tc.count)
			labels, err := (Labels{Client: transport.New(server.URL, "", false), Repository: "o/r"}).All(context.Background())
			require.NoError(t, err)
			require.Len(t, labels, tc.total)
			for i, label := range labels {
				require.Equal(t, int64(i+1), label.ID)
			}
			require.Equal(t, tc.requests, int(requests.Load()))
		})
	}
}

func TestPullListingsStopAtTheCountedEnd(t *testing.T) {
	listers := map[string]struct {
		limit int
		list  func(*transport.Client) (int, error)
	}{
		"open pulls": {50, func(client *transport.Client) (int, error) {
			items, err := (Pulls{Client: client, Repository: "o/r"}).ListOpen(context.Background())
			return len(items), err
		}},
		"changed files": {100, func(client *transport.Client) (int, error) {
			items, err := (Pulls{Client: client, Repository: "o/r"}).Files(context.Background(), 1)
			return len(items), err
		}},
		"reviews": {100, func(client *transport.Client) (int, error) {
			items, err := (Reviews{Client: client, Repository: "o/r"}).All(context.Background(), 1)
			return len(items), err
		}},
	}
	for name, lister := range listers {
		for _, tc := range []struct {
			total int
			count bool
		}{{40, true}, {100, true}, {120, true}, {120, false}} {
			t.Run(fmt.Sprintf("%s %+v", name, tc), func(t *testing.T) {
				server, requests := cappedServer(t, tc.total, 200, false, tc.count)
				client := transport.New(server.URL, "", false)
				// The instance's page size is read once per client, not once per listing.
				require.Equal(t, 200, maxPageSize(context.Background(), client))
				requests.Store(0)
				got, err := lister.list(client)
				require.NoError(t, err)
				require.Equal(t, tc.total, got)
				want := (tc.total + lister.limit - 1) / lister.limit
				if !tc.count {
					// Without a count only an empty page ends the listing.
					want++
				}
				require.Equal(t, want, int(requests.Load()))
			})
		}
	}
}

func TestCappedLaterPagesOfReleasesReviewsAndActions(t *testing.T) {
	ids := func(n int, id func(int) int64) []int64 {
		result := make([]int64, n)
		for i := range result {
			result[i] = id(i)
		}
		return result
	}
	wrapIn := func(field string) func(items string, total int) string {
		return func(items string, total int) string {
			return fmt.Sprintf(`{"total_count":%d,%q:%s}`, total, field, items)
		}
	}
	listers := map[string]struct {
		wrap func(items string, total int) string
		list func(client *transport.Client, limit, page int) ([]int64, error)
	}{
		"releases": {nil, func(client *transport.Client, limit, page int) ([]int64, error) {
			window, err := (Releases{Client: client, Repository: "o/r"}).ListPage(context.Background(), limit, page, ReleaseListOptions{})
			return ids(len(window.Items), func(i int) int64 { return window.Items[i].ID }), err
		}},
		"reviews": {nil, func(client *transport.Client, limit, page int) ([]int64, error) {
			window, err := (Reviews{Client: client, Repository: "o/r"}).ListPage(context.Background(), 1, limit, page)
			return ids(len(window.Items), func(i int) int64 { return window.Items[i].ID }), err
		}},
		"jobs": {wrapIn("jobs"), func(client *transport.Client, limit, page int) ([]int64, error) {
			window, err := (Actions{Client: client, Repository: "o/r"}).JobsPage(context.Background(), 1, limit, page)
			return ids(len(window.Items), func(i int) int64 { return window.Items[i].ID }), err
		}},
		"runs": {wrapIn("workflow_runs"), func(client *transport.Client, limit, page int) ([]int64, error) {
			runs, err := (Actions{Client: client, Repository: "o/r"}).Runs(context.Background(), RunListOptions{Limit: limit, Page: page})
			return ids(len(runs), func(i int) int64 { return runs[i].ID }), err
		}},
		"artifacts": {wrapIn("artifacts"), func(client *transport.Client, limit, page int) ([]int64, error) {
			window, err := (Actions{Client: client, Repository: "o/r"}).Artifacts(context.Background(), 1, limit, page)
			return ids(len(window.Items), func(i int) int64 { return window.Items[i].ID }), err
		}},
	}
	for name, lister := range listers {
		// The capped page 2 of 50 items accounts for the remainder of 120 after the first 70.
		for _, total := range []int{120, 100} {
			t.Run(fmt.Sprintf("%s of %d", name, total), func(t *testing.T) {
				wrap := lister.wrap
				if wrap == nil {
					wrap = func(items string, _ int) string { return items }
				}
				server, _ := wrappedCappedServer(t, total, 50, false, true, wrap)
				got, err := lister.list(transport.New(server.URL, "", false), 70, 2)
				require.NoError(t, err)
				require.Equal(t, ids(total-70, func(i int) int64 { return int64(71 + i) }), got)
			})
		}
	}
}

func TestShortLastPageWithLinksSkipsThePageSizeLookup(t *testing.T) {
	headers := http.Header{"X-Total-Count": {"12"}}
	headers.Set("Link", `<http://h/api/v1/o?limit=10&page=1>; rel="prev",<http://h/api/v1/o?limit=10&page=1>; rel="first"`)
	window, err := cappedPage(10, 2, func() int {
		t.Fatal("a last page with links but no next link needs no page size lookup")
		return 0
	}, func(limit, page int) ([]int, http.Header, error) {
		require.Equal(t, 10, limit)
		require.Equal(t, 2, page)
		return []int{11, 12}, headers, nil
	})
	require.NoError(t, err)
	require.Equal(t, []int{11, 12}, window.Items)
	require.False(t, window.More)
}

func TestPagesOfAServerThatHidesCountedItemsHoldEachItemOnce(t *testing.T) {
	for _, tc := range []struct {
		total, maxItems int
		hidden          func(id int) bool
		limits          []int
	}{
		{100, 50, func(id int) bool { return id%2 == 0 }, []int{30, 100}},
		{300, 100, func(id int) bool { return id%20 == 0 }, []int{30, 70, 100, 150, 200, 250}},
		{200, 50, func(id int) bool { return id > 50 && id%10 == 0 }, []int{150}},
	} {
		fetch := func(limit, page int) ([]int, http.Header, error) {
			size := min(limit, tc.maxItems)
			var items []int
			for id := (page-1)*size + 1; id <= min(page*size, tc.total); id++ {
				if !tc.hidden(id) {
					items = append(items, id)
				}
			}
			headers := http.Header{"X-Total-Count": {strconv.Itoa(tc.total)}}
			if page*size < tc.total {
				headers.Set("Link", fmt.Sprintf(`<http://h/api/v1/o?limit=%d&page=%d>; rel="next"`, limit, page+1))
			}
			return items, headers, nil
		}
		var want []int
		for id := 1; id <= tc.total; id++ {
			if !tc.hidden(id) {
				want = append(want, id)
			}
		}
		for _, limit := range tc.limits {
			t.Run(fmt.Sprintf("max %d limit %d of %d", tc.maxItems, limit, tc.total), func(t *testing.T) {
				var seen []int
				for page := 1; ; page++ {
					window, err := cappedPage(limit, page, func() int { return tc.maxItems }, fetch)
					require.NoError(t, err)
					seen = append(seen, window.Items...)
					if !window.More {
						break
					}
				}
				require.Equal(t, want, seen)
			})
		}
	}
}
