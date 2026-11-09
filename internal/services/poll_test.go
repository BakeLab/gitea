// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// outageServer serves a run that completes on the last of polls; the answers to the polls before
// it are given by status, where 0 means an in-progress run, -1 a dropped connection and -2 an
// answer that breaks off.
func outageServer(t *testing.T, statuses ...int) *Actions {
	t.Helper()
	polls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/workflows":
			_, _ = w.Write([]byte(`{"workflows":[]}`))
		case "/api/v1/repos/owner/repo/actions/runs/1":
			polls++
			if polls > len(statuses) {
				_, _ = w.Write([]byte(`{"id":1,"status":"completed","conclusion":"success"}`))
				return
			}
			switch status := statuses[polls-1]; status {
			case 0:
				_, _ = w.Write([]byte(`{"id":1,"status":"in_progress"}`))
			case -1:
				conn, _, err := http.NewResponseController(w).Hijack()
				require.NoError(t, err)
				_ = conn.Close()
			case -2:
				breakOff(w)
			default:
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"unavailable"}`))
			}
		case "/api/v1/repos/owner/repo/actions/runs/1/jobs":
			_, _ = w.Write([]byte(`{"jobs":[]}`))
		case "/api/v1/repos/owner/repo":
			_, _ = w.Write([]byte(`{"full_name":"owner/repo"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	client.Retries = 0
	return &Actions{Client: client, Repository: "owner/repo"}
}

// breakOff starts an answer and drops the connection before its body is complete.
func breakOff(w http.ResponseWriter) {
	w.Header().Set("Content-Length", "100")
	_, _ = w.Write([]byte(`{"id":1,`))
}

func TestWatchRidesOutAShortOutage(t *testing.T) {
	service := outageServer(t, 0, http.StatusServiceUnavailable, -1, -2, http.StatusBadGateway, http.StatusGatewayTimeout, 0)
	run, err := service.Watch(context.Background(), 1, time.Millisecond, nil)
	require.NoError(t, err)
	require.Equal(t, "completed", run.Status)
}

func TestWatchFailsAtOnceOnOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		want     string
	}{
		{"first poll", []int{http.StatusServiceUnavailable}, "HTTP 503"},
		{"not found", []int{0, http.StatusNotFound}, "run 1 not found"},
		{"unauthorized", []int{0, http.StatusUnauthorized}, "authentication rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := outageServer(t, tc.statuses...).Watch(context.Background(), 1, time.Millisecond, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestWatchGivesUpOnALongOutage(t *testing.T) {
	previous := pollOutage
	pollOutage = 20 * time.Millisecond
	t.Cleanup(func() { pollOutage = previous })
	statuses := []int{0}
	for range 1000 {
		statuses = append(statuses, http.StatusServiceUnavailable)
	}
	_, err := outageServer(t, statuses...).Watch(context.Background(), 1, time.Millisecond, nil)
	require.ErrorContains(t, err, "503")
}

func TestCheckWatchRidesOutAShortOutage(t *testing.T) {
	heads := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			heads++
			if heads == 2 || heads == 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"head":{"sha":"abc"}}`))
			return
		}
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		state := "pending"
		if heads > 3 {
			state = "success"
		}
		_, _ = w.Write([]byte(`[{"id":1,"context":"build","status":"` + state + `"}]`))
	}))
	client.Retries = 0
	items, err := Pulls{Client: client, Repository: "owner/repo"}.WatchChecks(context.Background(), 7, time.Millisecond, false, nil)
	require.NoError(t, err)
	require.Equal(t, "success", items[0].State)
}

func TestFollowLogsRidesOutAShortOutage(t *testing.T) {
	logPolls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/1/jobs":
			status := "in_progress"
			if logPolls >= 4 {
				status = "completed"
			}
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[{"id":2,"run_attempt":1,"name":"build","status":"` + status + `","started_at":"2028-01-01T00:00:00Z"}]}`))
		case "/api/v1/repos/owner/repo/actions/jobs/2/logs":
			logPolls++
			switch logPolls {
			case 1:
				_, _ = w.Write([]byte("line\n"))
				return
			case 2:
				w.WriteHeader(http.StatusBadGateway)
				return
			case 3:
				// The connection drops partway through the log.
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("line\nsec"))
				return
			}
			_, _ = w.Write([]byte("line\nsecond\n"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	client.Retries = 0
	var out strings.Builder
	err := Actions{Client: client, Repository: "owner/repo"}.FollowJobLogs(context.Background(), 1, 2, time.Millisecond, func(chunk []byte) error {
		out.Write(chunk)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "line\nsecond\n", out.String())
}

// notificationOutageServer serves the notifications of a repository; the answers to the polls are
// given by status, where 0 means a notification, 1 the same one changed, -1 a dropped connection
// and -2 an answer that breaks off. The polls after the last status get the changed notification.
func notificationOutageServer(t *testing.T, statuses ...int) Notifications {
	t.Helper()
	polls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/owner/repo/notifications" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		polls++
		status := 1
		if polls <= len(statuses) {
			status = statuses[polls-1]
		}
		switch status {
		case 0:
			_, _ = w.Write([]byte(`[{"id":1,"unread":true,"subject":{"title":"First"}}]`))
		case 1:
			_, _ = w.Write([]byte(`[{"id":1,"unread":true,"subject":{"title":"Changed"}}]`))
		case -1:
			conn, _, err := http.NewResponseController(w).Hijack()
			require.NoError(t, err)
			_ = conn.Close()
		case -2:
			breakOff(w)
		default:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"unavailable"}`))
		}
	}))
	client.Retries = 0
	return Notifications{Client: client, Repository: "owner/repo"}
}

func TestNotificationWatchRidesOutAShortOutage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := notificationOutageServer(t, 0, http.StatusServiceUnavailable, -1, -2, http.StatusBadGateway, http.StatusGatewayTimeout, 0)
	var titles []string
	err := service.Watch(ctx, NotificationOptions{Limit: 30, Page: 1}, time.Millisecond, func(items []Notification) error {
		titles = append(titles, items[0].Title)
		if len(titles) == 2 {
			cancel()
		}
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"First", "Changed"}, titles)
}

func TestNotificationWatchFailsAtOnceOnOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []int
		want     string
	}{
		{"first poll", []int{http.StatusServiceUnavailable}, "HTTP 503"},
		{"not found", []int{0, http.StatusNotFound}, "repository owner/repo not found"},
		{"unauthorized", []int{0, http.StatusUnauthorized}, "authentication rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := notificationOutageServer(t, tc.statuses...).Watch(context.Background(), NotificationOptions{Limit: 30, Page: 1}, time.Millisecond, func([]Notification) error { return nil })
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestNotificationWatchGivesUpOnALongOutage(t *testing.T) {
	previous := pollOutage
	pollOutage = 20 * time.Millisecond
	t.Cleanup(func() { pollOutage = previous })
	statuses := []int{0}
	for range 1000 {
		statuses = append(statuses, http.StatusServiceUnavailable)
	}
	err := notificationOutageServer(t, statuses...).Watch(context.Background(), NotificationOptions{Limit: 30, Page: 1}, time.Millisecond, func([]Notification) error { return nil })
	require.ErrorContains(t, err, "503")
}
