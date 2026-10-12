// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestFollowLogsEmitsOnlyAppendedBytesAndStops(t *testing.T) {
	snapshots := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
				return
			}
			status := "in_progress"
			if snapshots >= 2 {
				status = "completed"
			}
			_, _ = fmt.Fprintf(w, `{"jobs":[{"id":9,"status":%q}]}`, status)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			snapshots++
			if snapshots == 1 {
				_, _ = w.Write([]byte("first\n"))
			} else {
				_, _ = w.Write([]byte("first\nsecond\n"))
			}
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
	var output strings.Builder
	err := service.FollowJobLogs(context.Background(), 7, 9, time.Millisecond, func(chunk []byte) error { _, err := output.Write(chunk); return err })
	require.NoError(t, err)
	require.Equal(t, "first\nsecond\n", output.String())
	require.Equal(t, 3, snapshots)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = service.FollowJobLogs(ctx, 7, 9, time.Millisecond, func([]byte) error { t.Fatal("canceled follow emitted logs"); return nil })
	require.ErrorIs(t, err, context.Canceled)
}

func TestFollowLogsWaitsForQueuedJobAndRejectsChangedSnapshots(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/jobs"):
					if r.URL.Query().Get("page") != "1" {
						_, _ = w.Write([]byte(`{"jobs":[]}`))
						return
					}
					status := "queued"
					if reads > 0 {
						status = "in_progress"
					}
					if reads > 1 && !changed {
						status = "completed"
					}
					_, _ = fmt.Fprintf(w, `{"jobs":[{"id":9,"run_attempt":1,"status":%q}]}`, status)
				case strings.HasSuffix(r.URL.Path, "/logs"):
					reads++
					if reads == 1 {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					if reads > 2 && changed {
						_, _ = w.Write([]byte("other\nnew\n"))
						return
					}
					_, _ = w.Write([]byte("first\n"))
				}
			}))
			defer server.Close()
			service := Actions{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}
			var output strings.Builder
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := service.FollowJobLogs(ctx, 7, 9, time.Millisecond, func(chunk []byte) error { _, err := output.Write(chunk); return err })
			if changed {
				require.ErrorContains(t, err, "contents changed")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "first\n", output.String())
			require.Equal(t, 3, reads)
		})
	}
}

func TestJobsPageMapsJobsAndSendsPagination(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/jobs", r.URL.Path)
		require.Equal(t, "20", r.URL.Query().Get("limit"))
		require.Equal(t, "3", r.URL.Query().Get("page"))
		_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"id":9,"run_attempt":2,"name":"build","status":"completed","conclusion":"success","runner_name":"excluded"}]}`))
	}))
	jobs, err := (Actions{Client: client, Repository: "owner/repo"}).JobsPage(context.Background(), 7, 20, 3)
	require.NoError(t, err)
	require.Equal(t, []Job{{RunAttempt: 2, ID: 9, Name: "build", Status: "completed", Conclusion: "success"}}, jobs)
}

func TestRunLogsArchiveStreams(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/logs", r.URL.Path)
		_, _ = w.Write([]byte("PK archive"))
	}))
	body, err := (Actions{Client: client, Repository: "owner/repo"}).RunLogsArchive(context.Background(), 7)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, "PK archive", string(data))
}

func TestWaitForFirstJobPollsUntilJobsExist(t *testing.T) {
	polls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/owner/repo/actions/runs/7/jobs":
			polls++
			if polls < 3 || r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[{"id":11,"name":"build"},{"id":12,"name":"test"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := service.WaitForFirstJob(ctx, 7, time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, Job{ID: 11, Name: "build"}, job)
	_, err = service.WaitForFirstJob(ctx, 7, 0)
	require.ErrorContains(t, err, "interval must be positive")
}
