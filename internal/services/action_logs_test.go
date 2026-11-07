// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFollowLogsEmitsOnlyAppendedBytesAndStops(t *testing.T) {
	snapshots := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
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
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			reads := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			service := Actions{Client: client, Repository: "owner/repo"}
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
		if r.URL.Path == "/api/v1/settings/api" {
			_, _ = w.Write([]byte(`{"max_response_items":50}`))
			return
		}
		require.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/jobs", r.URL.Path)
		require.Equal(t, "20", r.URL.Query().Get("limit"))
		require.Equal(t, "3", r.URL.Query().Get("page"))
		w.Header().Set("X-Total-Count", "41")
		_, _ = w.Write([]byte(`{"total_count":41,"jobs":[{"id":9,"run_attempt":2,"name":"build","status":"completed","conclusion":"success","runner_name":"excluded",
			"labels":["ubuntu-latest"],"html_url":"https://gitea.test/owner/repo/actions/runs/7/jobs/9","created_at":"2028-01-01T00:00:00Z",
			"started_at":"2028-01-01T00:00:01Z","completed_at":"1970-01-01T00:00:00Z",
			"steps":[{"number":1,"name":"Set up job","status":"completed","conclusion":"success","started_at":"2028-01-01T00:00:01Z","completed_at":"2028-01-01T00:00:02Z"}]}]}`))
	}))
	jobs, err := (Actions{Client: client, Repository: "owner/repo"}).JobsPage(context.Background(), 7, 20, 3)
	require.NoError(t, err)
	started, stepStarted, stepCompleted := "2028-01-01T00:00:01Z", "2028-01-01T00:00:01Z", "2028-01-01T00:00:02Z"
	require.Equal(t, Page[Job]{Items: []Job{{
		RunAttempt: 2, ID: 9, Name: "build", Status: "completed", Conclusion: "success", URL: "https://gitea.test/owner/repo/actions/runs/7/jobs/9",
		Labels: []string{"ubuntu-latest"}, CreatedAt: "2028-01-01T00:00:00Z", StartedAt: &started,
		Steps: []Step{{Number: 1, Name: "Set up job", Status: "completed", Conclusion: "success", StartedAt: &stepStarted, CompletedAt: &stepCompleted}},
	}}, Total: 41, Counted: true}, jobs)
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

func TestRunLogsArchiveFollowsObjectStorageRedirect(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("PK archive"))
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/logs", r.URL.Path)
		assert.Equal(t, "token secret", r.Header.Get("Authorization"))
		http.Redirect(w, r, storage.URL+"/bucket/logs.zip?X-Amz-Signature=value", http.StatusFound)
	}))
	defer server.Close()
	body, err := (Actions{Client: transport.New(server.URL, "secret", false), Repository: "owner/repo"}).RunLogsArchive(context.Background(), 7)
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
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
	require.Equal(t, int64(11), job.ID)
	require.Equal(t, "build", job.Name)
	_, err = service.WaitForFirstJob(ctx, 7, 0)
	require.ErrorContains(t, err, "interval must be positive")
}
