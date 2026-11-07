// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/require"
)

func TestPullStatusFiltersRequestedReviewsBeforeLimiting(t *testing.T) {
	pages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/repos/alice/project/issues":
			require.Equal(t, "pulls", r.URL.Query().Get("type"))
			require.Equal(t, "open", r.URL.Query().Get("state"))
			require.Equal(t, "1", r.URL.Query().Get("limit"))
			require.True(t, r.URL.Query().Get("created_by") == "alice" || r.URL.Query().Get("assigned_by") == "alice")
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/issues/search":
			require.Equal(t, "true", r.URL.Query().Get("review_requested"))
			require.Equal(t, "alice", r.URL.Query().Get("owner"))
			require.Equal(t, "100", r.URL.Query().Get("limit"))
			page := r.URL.Query().Get("page")
			pages = append(pages, page)
			switch page {
			case "1":
				_, _ = w.Write([]byte(`[{"id":1,"number":1,"repository":{"full_name":"alice/other"}}]`))
			case "2":
				_, _ = w.Write([]byte(`[{"id":2,"number":2,"repository":{"full_name":"Alice/Project"}},{"id":3,"number":3,"repository":{"full_name":"alice/project"}}]`))
			default:
				t.Fatal("status fetched beyond the requested limit")
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result, err := (Status{Client: transport.New(server.URL, "token", false), Repository: "alice/project"}).Pulls(context.Background(), 1)
	require.NoError(t, err)
	require.Empty(t, result.Authored)
	require.Empty(t, result.Assigned)
	require.Len(t, result.ReviewRequested, 1)
	require.Equal(t, int64(2), result.ReviewRequested[0].ID)
	require.Equal(t, []string{"1", "2"}, pages)
}

func TestStatusSummarySkipsRunsWhenActionsAreDisabled(t *testing.T) {
	accounts := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/user":
			accounts++
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"has_actions":false}`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte(`[{"id":4,"number":4}]`))
		case r.URL.Path == "/api/v1/repos/issues/search":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	result, err := (Status{Client: client, Repository: "alice/project"}).Summary(context.Background(), 30, "alice/project", false)
	require.NoError(t, err)
	require.False(t, result.RunsIncluded)
	require.Empty(t, result.FailingRuns)
	require.Len(t, result.AssignedIssues, 1)
	require.Equal(t, 1, accounts)
}

func TestStatusSearchesTogetherAndKeepTheirResults(t *testing.T) {
	var accounts atomic.Int32
	var searches sync.WaitGroup
	searches.Add(5) // pulls authored and assigned, assigned issues, and mentions of both kinds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user":
			accounts.Add(1)
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/repos/alice/project/issues":
			searches.Done()
			waitTogether(t, &searches)
			query := r.URL.Query()
			number := map[bool]string{true: "1", false: "2"}[query.Get("type") == "pulls"]
			_, _ = fmt.Fprintf(w, `[{"id":%s,"number":%s,"repository":{"full_name":"alice/project"}}]`, number, number)
		case "/api/v1/repos/issues/search":
			// The whole result fits the first page, which the count tells without asking for another.
			require.Equal(t, "1", r.URL.Query().Get("page"))
			w.Header().Set("X-Total-Count", "1")
			_, _ = w.Write([]byte(`[{"id":3,"number":3,"repository":{"full_name":"alice/other"}}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := transport.New(server.URL, "token", false)
	result, err := (Status{Client: client, Repository: "alice/project"}).Summary(t.Context(), 5, "", false)
	require.NoError(t, err)
	require.Equal(t, int32(1), accounts.Load(), "concurrent searches must not each look up the account")
	require.Len(t, result.PullRequests.Authored, 1)
	require.Len(t, result.PullRequests.Assigned, 1)
	require.Empty(t, result.PullRequests.ReviewRequested)
	require.Len(t, result.AssignedIssues, 1)
	require.Len(t, result.Mentions, 2)
	require.Equal(t, "alice", result.User)
}

func TestPullStatusReportsAFailureOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	_, err := (Status{Client: transport.New(server.URL, "token", false)}).Pulls(t.Context(), 5)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "\n", "joined errors would repeat one cause per request")
}
