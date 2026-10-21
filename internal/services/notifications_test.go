// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
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

func TestNotificationWatchReportsABurstLargerThanAPage(t *testing.T) {
	for name, readEarlier := range map[string]bool{"earlier ones listed": false, "earlier ones read": true} {
		t.Run(name, func(t *testing.T) {
			// Threads 1-5 exist when watching starts; 6-10 arrive before the second poll.
			// Gitea lists them newest first.
			ids := []int{5, 4, 3, 2, 1}
			polls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/settings/api" {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if page == 1 {
					polls++
					if polls == 4 {
						cancel()
					}
					if polls == 2 {
						ids = append([]int{10, 9, 8, 7, 6}, ids...)
						if readEarlier {
							ids = slices.DeleteFunc(ids, func(id int) bool { return id == 5 || id == 4 })
						}
					}
				}
				threads := []string{}
				for _, id := range ids[min(len(ids), (page-1)*limit):min(len(ids), page*limit)] {
					threads = append(threads, fmt.Sprintf(`{"id":%d,"updated_at":"2026-10-01T00:00:%02dZ","subject":{"title":"t%d"}}`, id, id, id))
				}
				w.Header().Set("X-Total-Count", strconv.Itoa(len(ids)))
				_, _ = w.Write([]byte("[" + strings.Join(threads, ",") + "]"))
			}))
			var batches [][]int64
			err := (Notifications{Client: client}).Watch(ctx, NotificationOptions{Limit: 2, Page: 1}, time.Millisecond, func(items []Notification) error {
				batch := []int64{}
				for _, item := range items {
					batch = append(batch, item.ID)
				}
				batches = append(batches, batch)
				return nil
			})
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, [][]int64{{5, 4}, {10, 9, 8, 7, 6}}, batches)
		})
	}
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

func TestNotificationMarkNamesTheMissingNotification(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"notification does not exist [id: 99]"}`))
	}))
	_, err := (Notifications{Client: client}).Mark(context.Background(), 99, "read")
	require.EqualError(t, err, "notification 99 not found")
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
	require.Equal(t, []Notification{{ID: 2, Repository: "alice/project", Title: "Bug", Type: "issue", State: "open"}}, items)
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
	page, err := (Notifications{Client: client}).List(context.Background(), NotificationOptions{Limit: 20, Page: 2, Types: []string{"Issue", "Pull"}, Since: &since})
	require.NoError(t, err)
	require.Equal(t, []Notification{}, page.Items)
}

func TestNotificationFromMapsServerFields(t *testing.T) {
	thread := decode[gitea.NotificationThread](t, `{"id":5,"repository":{"full_name":"alice/project"},"unread":true,"pinned":true,
		"updated_at":"2026-10-02T12:00:00+02:00","subject":{"title":"Fix","html_url":"https://gitea.test/alice/project/pulls/1","type":"Pull","state":"merged",
		"url":"https://gitea.test/api/v1/repos/alice/project/issues/1","latest_comment_url":"https://gitea.test/api/v1/repos/alice/project/issues/comments/9"}}`)
	require.Equal(t, Notification{
		ID: 5, Repository: "alice/project", Number: 1, Title: "Fix", URL: "https://gitea.test/alice/project/pulls/1", Type: "pull", State: "merged",
		Unread: true, Pinned: true, UpdatedAt: "2026-10-02T12:00:00+02:00", latestComment: "https://gitea.test/api/v1/repos/alice/project/issues/comments/9",
	}, notificationFrom(thread))
	commit := decode[gitea.NotificationThread](t, `{"id":7,"subject":{"type":"Commit","url":"https://gitea.test/api/v1/repos/alice/project/commits/123"}}`)
	require.Zero(t, notificationFrom(commit).Number)
	require.Equal(t, Notification{ID: 6}, notificationFrom(decode[gitea.NotificationThread](t, `{"id":6,"repository":null,"subject":null}`)))
}
