// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gitea.dev/tea/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestRunTimeBoundsUseOneClockReading(t *testing.T) {
	now := time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC)
	deps, _, _ := testDeps(t)
	reads := 0
	deps.Now = func() time.Time { reads++; return now }
	err := Execute(context.Background(), deps, []string{"run", "list", "--since=1h", "--until=2h"})
	require.ErrorContains(t, err, "--since must not be later than --until")
	require.Equal(t, 1, reads)
}

func TestRunWatchExitAndOutputContracts(t *testing.T) {
	for _, test := range []struct {
		name, status, conclusion string
		code                     int
	}{
		{"success", "completed", "success", 0},
		{"failed run", "completed", "failure", clierr.Failure},
		{"timeout", "in_progress", "", clierr.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/actions/runs/7", r.URL.Path)
				_, _ = w.Write([]byte(`{"id":7,"status":"` + test.status + `","conclusion":"` + test.conclusion + `"}`))
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, []string{"run", "watch", "7", "--repo", "alice/project", "--json=id,status,conclusion", "--interval=1ms", "--timeout=100ms", "--no-input"})
			require.Equal(t, test.code, clierr.ExitCode(err))
			if test.code == clierr.Canceled {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Empty(t, out.String())
			} else {
				require.JSONEq(t, `{"id":7,"status":"completed","conclusion":"`+test.conclusion+`"}`, out.String())
			}
			require.Contains(t, diagnostics.String(), "run 7:")
		})
	}
}

func TestWorkflowRunFollowStreamsFirstJobLogs(t *testing.T) {
	jobPolls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/actions/workflows/ci.yml/dispatches":
			require.Equal(t, "true", r.URL.Query().Get("return_run_details"))
			_, _ = w.Write([]byte(`{"workflow_run_id":11,"html_url":"https://gitea.test/owner/repo/actions/runs/11"}`))
		case r.URL.Path == "/api/v1/repos/owner/repo/actions/runs/11/jobs":
			jobPolls++
			if r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[{"id":21,"name":"build","status":"completed","conclusion":"success"}]}`))
		case r.URL.Path == "/api/v1/repos/owner/repo/actions/jobs/21/logs":
			_, _ = w.Write([]byte("hello from job\n"))
		case r.URL.Path == "/api/v1/repos/owner/repo/actions/runs/11":
			_, _ = w.Write([]byte(`{"id":11,"status":"completed","conclusion":"success","display_title":"ci"}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"workflow", "run", "ci.yml", "-R", "owner/repo", "--ref", "main", "--follow", "--no-input"}))
	require.Contains(t, out.String(), "11\thttps://gitea.test/owner/repo/actions/runs/11")
	require.Contains(t, out.String(), "hello from job\n")
	require.Contains(t, diagnostics.String(), "Waiting for run 11 jobs")
	require.Contains(t, diagnostics.String(), "Following job 21 (build)")
	require.GreaterOrEqual(t, jobPolls, 1)

	err := Execute(context.Background(), deps, []string{"workflow", "run", "ci.yml", "-R", "owner/repo", "--ref", "main", "--follow", "--json", "--no-input"})
	require.Error(t, err)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Contains(t, err.Error(), "--follow cannot be combined with --json")
}
