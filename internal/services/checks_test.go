// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestCheckWatchRevalidatesHeadBeforeReturning(t *testing.T) {
	heads := 0
	newSnapshots := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			heads++
			sha := "old"
			if heads > 1 {
				sha = "new"
			}
			_, _ = w.Write([]byte(`{"head":{"sha":"` + sha + `"}}`))
			return
		}
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/old") {
			_, _ = w.Write([]byte(`[{"id":1,"context":"build","status":"success"}]`))
			return
		}
		require.True(t, strings.HasSuffix(r.URL.Path, "/new"))
		newSnapshots++
		state := "pending"
		if newSnapshots > 1 {
			state = "success"
		}
		_, _ = w.Write([]byte(`[{"id":2,"context":"build","status":"` + state + `"}]`))
	}))
	defer server.Close()
	service := Pulls{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	items, err := service.WatchChecks(ctx, 7, time.Millisecond, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "new", items[0].SHA)
	require.Equal(t, "success", items[0].State)
	require.Equal(t, 2, newSnapshots)
	require.Equal(t, 5, heads)
}

func TestCheckWatchTreatsSkippedAsPassing(t *testing.T) {
	snapshots := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			_, _ = w.Write([]byte(`{"head":{"sha":"abc"}}`))
			return
		}
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		snapshots++
		state := "pending"
		if snapshots > 1 {
			state = "success"
		}
		_, _ = w.Write([]byte(`[{"id":1,"context":"lint","status":"skipped"},{"id":2,"context":"test","status":"` + state + `"}]`))
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	items, err := (Pulls{Client: client, Repository: "owner/repo"}).WatchChecks(ctx, 7, time.Millisecond, nil)
	require.NoError(t, err)
	require.Equal(t, 2, snapshots)
	require.Equal(t, "success", items[1].State)
}

func TestCheckFromMapsCommitStatuses(t *testing.T) {
	status := decode[gitea.Status](t, `{"id":3,"context":"ci/build","status":"failure","description":"Broken","target_url":"https://ci.test/3",
		"url":"https://gitea.test/api/v1/statuses/3","created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-01T12:05:00Z"}`)
	require.Equal(t, Check{
		SHA: "abc", ID: 3, Name: "ci/build", State: "failure", Description: "Broken", URL: "https://ci.test/3",
		CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-01T12:05:00Z",
	}, checkFrom("abc", status))
}
