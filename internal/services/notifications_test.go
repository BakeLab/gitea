// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestNotificationWatchOnlyEmitsChanges(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/alice/project/notifications", r.URL.Path)
		require.Equal(t, []string{"unread", "pinned"}, r.URL.Query()["status-types"])
		if calls < 3 {
			_, _ = w.Write([]byte(`[{"id":1,"unread":true,"subject":{"title":"First"}}]`))
		} else {
			_, _ = w.Write([]byte(`[{"id":1,"unread":true,"subject":{"title":"Changed"}}]`))
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := Notifications{Client: client, Repository: "alice/project"}
	batches := [][]Notification{}
	err := service.Watch(ctx, NotificationOptions{Limit: 30, Page: 1, Statuses: []string{"unread", "pinned"}}, time.Millisecond, func(items []Notification) error {
		batches = append(batches, items)
		if len(batches) == 2 {
			cancel()
		}
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, batches, 2)
	require.Equal(t, "Changed", batches[1][0].Title)
}

func TestNotificationRepositoryMismatchPreventsMutation(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_, _ = w.Write([]byte(`{"id":1,"repository":{"full_name":"bob/other"}}`))
	}))
	service := Notifications{Client: client, Repository: "alice/project"}
	_, err := service.Mark(context.Background(), 1, "read")
	require.ErrorContains(t, err, "not selected repository")
	require.Zero(t, mutations)
}

func TestNotificationRepositoryMatchIgnoresCase(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_, _ = w.Write([]byte(`{"id":1,"repository":{"full_name":"Alice/Project"}}`))
	}))
	_, err := (Notifications{Client: client, Repository: "alice/project"}).Mark(context.Background(), 1, "read")
	require.NoError(t, err)
	require.Equal(t, 1, mutations)
}

func TestNotificationWatchRejectsNonPositiveInterval(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	err := (Notifications{Client: client}).Watch(context.Background(), NotificationOptions{Limit: 30, Page: 1}, 0, func([]Notification) error { return nil })
	require.ErrorContains(t, err, "interval must be positive")
}

func TestNotificationMarkAllScopesRepositoryAndFilters(t *testing.T) {
	before := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/notifications", r.URL.Path)
		require.Equal(t, "read", r.URL.Query().Get("to-status"))
		require.Equal(t, []string{"unread", "pinned"}, r.URL.Query()["status-types"])
		require.Equal(t, "2026-10-01T12:00:00Z", r.URL.Query().Get("last_read_at"))
		w.WriteHeader(http.StatusResetContent)
		_, _ = w.Write([]byte(`[{"id":2,"repository":{"full_name":"alice/project"},"subject":{"title":"Bug","type":"Issue","state":"open"}}]`))
	}))
	items, err := (Notifications{Client: client, Repository: "alice/project"}).MarkAll(context.Background(), "read", []string{"unread", "pinned"}, before)
	require.NoError(t, err)
	require.Equal(t, []Notification{{ID: 2, Repository: "alice/project", Title: "Bug", Type: "Issue", State: "open"}}, items)
}

func TestNotificationListSendsFilters(t *testing.T) {
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		query := r.URL.Query()
		require.Equal(t, "20", query.Get("limit"))
		require.Equal(t, "2", query.Get("page"))
		require.Equal(t, []string{"Issue", "Pull"}, query["subject-type"])
		require.Equal(t, "2026-10-01T00:00:00Z", query.Get("since"))
		require.Empty(t, query.Get("before"))
		_, _ = w.Write([]byte(`[]`))
	}))
	items, err := (Notifications{Client: client}).List(context.Background(), NotificationOptions{Limit: 20, Page: 2, Types: []string{"Issue", "Pull"}, Since: &since})
	require.NoError(t, err)
	require.Equal(t, []Notification{}, items)
}

func TestNotificationFromMapsServerFields(t *testing.T) {
	thread := decode[gitea.NotificationThread](t, `{"id":5,"repository":{"full_name":"alice/project"},"unread":true,"pinned":true,
		"updated_at":"2026-10-02T12:00:00+02:00","subject":{"title":"Fix","html_url":"https://gitea.test/alice/project/pulls/1","type":"Pull","state":"merged"}}`)
	require.Equal(t, Notification{
		ID: 5, Repository: "alice/project", Title: "Fix", URL: "https://gitea.test/alice/project/pulls/1", Type: "Pull", State: "merged",
		Unread: true, Pinned: true, UpdatedAt: "2026-10-02T12:00:00+02:00",
	}, notificationFrom(thread))
	require.Equal(t, Notification{ID: 6}, notificationFrom(decode[gitea.NotificationThread](t, `{"id":6,"repository":null,"subject":null}`)))
}
