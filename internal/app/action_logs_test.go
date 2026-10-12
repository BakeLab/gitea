// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunLogsStreamWithoutMixingDiagnosticsAndVerifyJobMembership(t *testing.T) {
	downloads := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/7/jobs":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"jobs":[{"id":9,"name":"Build","started_at":"2028-01-01T00:00:00Z"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
			}
		case "/api/v1/repos/owner/repo/actions/runs/7/logs":
			_, _ = w.Write([]byte("ZIP archive bytes"))
		case "/api/v1/repos/owner/repo/actions/jobs/9/logs":
			downloads++
			_, _ = w.Write([]byte("exact log\n"))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	args := []string{"run", "logs", "7", "-R", "owner/repo", "--job=9", "--no-input"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "exact log\n", out.String())
	require.Equal(t, "Build (job 9)\n", diagnostics.String())
	require.Equal(t, 1, downloads)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "7", "-R", "owner/repo", "--limit=20", "--page=1", "--json=id,name,run_attempt"}))
	require.JSONEq(t, `[{"id":9,"name":"Build","run_attempt":0}]`, out.String())
	require.Empty(t, diagnostics.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "jobs", "7", "-R", "owner/repo", "--page=2", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	out.Reset()
	diagnostics.Reset()
	args[5] = "--job=10"
	err := Execute(context.Background(), deps, args)
	require.EqualError(t, err, "job 10 not found in run 7")
	require.Equal(t, 1, downloads)
	require.Empty(t, out.String())
	path := filepath.Join(t.TempDir(), "logs.zip")
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--output-file", path}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "ZIP archive bytes", string(data))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

func TestRunLogsKeepOnlyColorsOnATerminal(t *testing.T) {
	const logs = "\x1b[36mstep\x1b[0m \x1b]0;PWNED\x07done\x1b[2J\r\n"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/7/jobs":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"jobs":[{"id":9,"name":"Build\u001b]0;PWNED\u0007","started_at":"2028-01-01T00:00:00Z"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
			}
		case "/api/v1/repos/owner/repo/actions/jobs/9/logs":
			_, _ = w.Write([]byte(logs))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	for tty, want := range map[bool]string{false: logs, true: "\x1b[36mstep\x1b[0m ]0;PWNEDdone[2J\n"} {
		deps, out, diagnostics := serverDeps(t, server.URL)
		deps.Streams.OutTTY = tty
		require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--job=9", "--no-input"}))
		require.Equal(t, want, out.String(), "tty=%t", tty)
		require.Equal(t, "Build]0;PWNED (job 9)\n", diagnostics.String())
	}
}

func TestRunLogsContinuePastJobsThatHaveNotStarted(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/actions/runs/7/jobs":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"jobs":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":8,"name":"lint","status":"completed","conclusion":"skipped","started_at":"1970-01-01T00:00:00Z","completed_at":"2028-01-01T00:01:00Z"},
				{"id":9,"name":"build","status":"queued","started_at":"1970-01-01T00:00:00Z","completed_at":"1970-01-01T00:00:00Z"},
				{"id":10,"name":"test","status":"in_progress","started_at":"2028-01-01T00:00:00Z"},
				{"id":11,"name":"deploy","status":"in_progress","started_at":"2028-01-01T00:00:00Z"}]}`))
		case "/api/v1/repos/owner/repo/actions/jobs/10/logs":
			_, _ = w.Write([]byte("test output\n"))
		case "/api/v1/repos/owner/repo/actions/jobs/11/logs":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"job not started"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--no-input"}))
	require.Equal(t, "test output\n", out.String())
	require.Equal(t, "lint (job 8): never started (skipped)\nbuild (job 9): not started yet\ntest (job 10)\ndeploy (job 11): not started yet\n", diagnostics.String())
}

func TestRunLogsSelectJobsAndFollowSeveral(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/jobs": failedJobsFixture,
		"GET actions/jobs/30/logs": "compiled\nlinked",
		"GET actions/jobs/31/logs": "boom\n",
	})
	for _, tc := range []struct {
		name          string
		args          []string
		out, progress string
		err           string
	}{
		{"failed jobs", []string{"--failed"}, "boom\n", "test (job 31)\n", ""},
		{"a job by name", []string{"--job", "Build"}, "compiled\nlinked", "build (job 30)\n", ""},
		{"follow several jobs", []string{"--follow", "--interval=1ms"}, "build\tcompiled\nbuild\tlinked\ntest\tboom\n", "build (job 30)\ntest (job 31)\n", ""},
		{"unknown job", []string{"--job", "deploy"}, "", "", `job "deploy" not found in run 29`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := actionsDeps(t, server.URL, false)
			err := Execute(context.Background(), deps, append([]string{"run", "logs", "29", "-R", "alice/project"}, tc.args...))
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.out, out.String())
			require.Equal(t, tc.progress, diagnostics.String())
		})
	}
}
