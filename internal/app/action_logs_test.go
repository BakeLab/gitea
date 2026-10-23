// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/cli/internal/clierr"
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
		case "/api/v1/repos/owner/repo/actions/jobs/10":
			_, _ = w.Write([]byte(`{"id":10,"run_id":8,"name":"Build"}`))
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
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--output", path}))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "ZIP archive bytes", string(data))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--output", path, "--skip-existing"}))
	require.Empty(t, out.String())
	require.Equal(t, "! Skipped the logs of run 7: "+path+" exists\n", diagnostics.String())
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
	for _, tc := range []struct {
		name       string
		tty, plain bool
		env        map[string]string
		want       string
	}{
		{"piped", false, false, nil, logs},
		{"piped with color always", false, false, map[string]string{"GITEA_CLI_COLOR": "always"}, logs},
		// The colors end with the log, which may stop in the middle of colored text.
		{"terminal", true, false, nil, "\x1b[36mstep\x1b[0m ]0;PWNEDdone[2J\n\x1b[0m"},
		// Without color the colors are dropped whole, and other escape sequences stay visible.
		{"NO_COLOR", true, false, map[string]string{"NO_COLOR": "1"}, "step ]0;PWNEDdone[2J\n"},
		{"dumb terminal", true, false, map[string]string{"TERM": "dumb"}, "step ]0;PWNEDdone[2J\n"},
		{"console without escape sequences", true, true, nil, "step ]0;PWNEDdone[2J\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := serverDeps(t, server.URL)
			deps.Getenv = serverEnv(server.URL, tc.env)
			deps.Streams.OutTTY, deps.Streams.OutPlain = tc.tty, tc.plain
			require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", "7", "-R", "owner/repo", "--job=9", "--no-input"}))
			require.Equal(t, tc.want, out.String())
			require.Equal(t, "Build]0;PWNED (job 9)\n", diagnostics.String())
			// gt run view --log prints the logs the same way.
			out.Reset()
			require.NoError(t, Execute(context.Background(), deps, []string{"run", "view", "7", "-R", "owner/repo", "--log", "--no-input"}))
			require.Equal(t, tc.want, out.String())
		})
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

func TestRunLogsAndRerunSelectTheJobOfAJobURL(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/runs/29":                failedRunFixture,
		"GET actions/runs/29/jobs":           failedJobsFixture,
		"GET actions/jobs/30/logs":           "compiled\n",
		"POST actions/runs/29/jobs/31/rerun": `{"id":31}`,
	})
	jobURL := func(job int) string { return fmt.Sprintf("%s/alice/project/actions/runs/29/jobs/%d", server.URL, job) }
	deps, out, _ := actionsDeps(t, server.URL, false)
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", jobURL(30)}))
	require.Equal(t, "compiled\n", out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", jobURL(30), "--job", "30"}))
	// --job may also name the job of the URL.
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "logs", jobURL(30), "--job", "build"}))
	require.Equal(t, "compiled\n", out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "rerun", jobURL(31)}))
	require.Contains(t, *requests, "POST actions/runs/29/jobs/31/rerun")
	*requests = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"run", "rerun", jobURL(31), "--job", "test"}))
	require.Contains(t, *requests, "POST actions/runs/29/jobs/31/rerun")
	for _, tc := range []struct {
		args []string
		err  string
	}{
		{[]string{"run", "logs", jobURL(30), "--job", "test"}, "--job test cannot be combined with the URL of job 30"},
		{[]string{"run", "rerun", jobURL(31), "--job", "build"}, "--job build cannot be combined with the URL of job 31"},
		{[]string{"run", "logs", jobURL(30), "--failed"}, "--failed cannot be combined with the URL of job 30"},
		{[]string{"run", "logs", jobURL(30), "--output", "logs.zip"}, "--output cannot be combined with the URL of job 30"},
		{[]string{"run", "rerun", jobURL(31), "--failed"}, "--failed cannot be combined with the URL of job 31"},
	} {
		err := Execute(context.Background(), deps, tc.args)
		require.EqualError(t, err, tc.err)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	}
}

func TestRunLogsSelectAJobOfAnEarlierAttempt(t *testing.T) {
	const earlier = `{"id":20,"run_id":29,"run_attempt":1,"name":"test","status":"completed","conclusion":"failure",
		"started_at":"2028-01-01T10:00:05Z","completed_at":"2028-01-01T10:00:50Z"}`
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/jobs": failedJobsFixture,
		"GET actions/jobs/20":      earlier,
		"GET actions/jobs/20/logs": "first attempt\n",
	})
	jobURL := server.URL + "/alice/project/actions/runs/29/jobs/20"
	for _, args := range [][]string{
		{"run", "logs", "29", "-R", "alice/project", "--job", "20"},
		{"run", "logs", jobURL},
		{"run", "logs", jobURL, "--follow", "--interval=1ms"},
	} {
		deps, out, diagnostics := actionsDeps(t, server.URL, false)
		require.NoError(t, Execute(context.Background(), deps, args), "%v", args)
		require.Equal(t, "first attempt\n", out.String(), "%v", args)
		require.Equal(t, "test (job 20)\n", diagnostics.String(), "%v", args)
	}
}

func TestRunLogsSelectJobsAndFollowSeveral(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/jobs": failedJobsFixture,
		"GET actions/jobs/30/logs": "compiled\nlinked",
		"GET actions/jobs/31/logs": "go test\nboom\n",
	})
	for _, tc := range []struct {
		name          string
		args          []string
		out, progress string
		err           string
	}{
		{"failed jobs", []string{"--failed"}, "go test\nboom\n", "test (job 31)\n", ""},
		{"the end of failed jobs", []string{"--failed", "--tail", "1"}, "boom\n", "test (job 31)\n", ""},
		{"the end of a job without a last line break", []string{"--job", "Build", "--tail", "1"}, "linked", "build (job 30)\n", ""},
		{"a tail longer than the log", []string{"--failed", "--tail", "5"}, "go test\nboom\n", "test (job 31)\n", ""},
		{"tail while following", []string{"--follow", "--tail", "1"}, "", "", "--follow cannot be combined with --tail"},
		{"a job by name", []string{"--job", "Build"}, "compiled\nlinked", "build (job 30)\n", ""},
		{"follow several jobs", []string{"--follow", "--interval=1ms"}, "build\tcompiled\nbuild\tlinked\ntest\tgo test\ntest\tboom\n", "build (job 30)\ntest (job 31)\n", ""},
		{"unknown job", []string{"--job", "deploy"}, "", "", `job "deploy" not found in run 29`},
		{"failed with a job", []string{"--job", "Build", "--failed"}, "", "", "--failed cannot be combined with --job"},
		{"clobber without a download", []string{"--clobber"}, "", "", "--clobber requires --output or --dir"},
		{"clobber to standard output", []string{"--clobber", "--output", "-"}, "", "", "--clobber cannot be combined with --output -"},
		{"skip-existing without a download", []string{"--failed", "--skip-existing"}, "", "", "--skip-existing requires --output or --dir"},
		{"skip-existing to standard output", []string{"--skip-existing", "--output", "-"}, "", "", "--skip-existing cannot be combined with --output -"},
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
