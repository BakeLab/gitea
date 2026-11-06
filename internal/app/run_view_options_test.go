// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestRunViewLogFailedAndJob(t *testing.T) {
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
		{"the failed jobs", []string{"--log-failed"}, "go test\nboom\n", "test (job 31)\n", ""},
		{"one job", []string{"--log", "--job", "build"}, "compiled\nlinked", "build (job 30)\n", ""},
		{"one job by ID", []string{"--log", "-j", "31"}, "go test\nboom\n", "test (job 31)\n", ""},
		{"an unknown job", []string{"--log", "--job", "deploy"}, "", "", `job "deploy" not found in run 29`},
		{"log and log-failed", []string{"--log", "--log-failed"}, "", "", "--log cannot be combined with --log-failed"},
		{"log-failed and a job", []string{"--log-failed", "--job", "build"}, "", "", "--log-failed cannot be combined with --job"},
		{"log-failed as JSON", []string{"--log-failed", "--json"}, "", "", "--log and --log-failed cannot be combined with --web, --json, --jq or --csv"},
		{"a job in the browser", []string{"--web", "--job", "build"}, "", "", "--web cannot be combined with --job"},
		{"no jobs and a job", []string{"--no-jobs", "--job", "build"}, "", "", "--no-jobs cannot be combined with --job, --log or --log-failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := actionsDeps(t, server.URL, false)
			err := Execute(context.Background(), deps, append([]string{"run", "view", "29", "-R", "alice/project", "--no-input"}, tc.args...))
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

func TestRunViewSelectsTheJobOfAJobURL(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET actions/runs/29":      failedRunFixture,
		"GET actions/runs/29/jobs": failedJobsFixture,
		"GET actions/jobs/30/logs": "compiled\n",
		"GET actions/workflows":    workflowsFixture,
	})
	jobURL := server.URL + "/alice/project/actions/runs/29/jobs/30"
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"the view", []string{jobURL, "--json", "jobs", "--jq", "[.jobs[].name]"}, `["build"]` + "\n"},
		{"the view with its --job name", []string{jobURL, "--job", "build", "--json", "jobs", "--jq", "[.jobs[].name]"}, `["build"]` + "\n"},
		{"the logs", []string{jobURL, "--log"}, "compiled\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, _ := actionsDeps(t, server.URL, false)
			require.NoError(t, Execute(context.Background(), deps, append([]string{"run", "view"}, tc.args...)))
			require.Equal(t, tc.want, out.String())
		})
	}
	for _, args := range [][]string{{jobURL, "--job", "test"}, {jobURL, "--job", "test", "--log"}} {
		deps, _, _ := actionsDeps(t, server.URL, false)
		err := Execute(context.Background(), deps, append([]string{"run", "view"}, args...))
		require.EqualError(t, err, "--job test cannot be combined with the URL of job 30")
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	}
	*requests = nil
	deps, _, _ := actionsDeps(t, server.URL, false)
	err := Execute(context.Background(), deps, []string{"run", "view", jobURL, "--log-failed"})
	require.EqualError(t, err, "--log-failed cannot be combined with the URL of job 30")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.Empty(t, *requests)
}

func TestRunViewLogFailedSaysARunIsStillRunning(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/jobs": `{"total_count":2,"jobs":[{"id":30,"name":"build","status":"completed","conclusion":"success"},{"id":31,"name":"test","status":"in_progress"}]}`,
	})
	deps, _, _ := actionsDeps(t, server.URL, false)
	require.EqualError(t, Execute(context.Background(), deps, []string{"run", "view", "29", "-R", "alice/project", "--log-failed", "--no-input"}), "run 29 has no failed or canceled jobs yet; it is still running")
}

func TestRunViewLogFailedNeedsAFailedJob(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/runs/29/jobs": `{"total_count":1,"jobs":[{"id":30,"name":"build","status":"completed","conclusion":"success"}]}`,
	})
	deps, _, _ := actionsDeps(t, server.URL, false)
	require.EqualError(t, Execute(context.Background(), deps, []string{"run", "view", "29", "-R", "alice/project", "--log-failed", "--no-input"}), "run 29 has no failed or canceled jobs")
}
