// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestStatusJSONAndActionsScope(t *testing.T) {
	runs := 0
	actionsEnabled := true
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project":
			if actionsEnabled {
				_, _ = w.Write([]byte(`{"has_actions":true}`))
			} else {
				_, _ = w.Write([]byte(`{"has_actions":false}`))
			}
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			runs++
			require.Equal(t, "/api/v1/repos/alice/project/actions/runs", r.URL.Path)
			require.Equal(t, []string{"failure", "timed_out"}, r.URL.Query()["status"])
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":7,"display_title":"Broken build","status":"completed","conclusion":"failure"}]}`))
		case strings.HasSuffix(r.URL.Path, "/issues") || strings.HasSuffix(r.URL.Path, "/search"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"status", "-R", "alice/project", "--json=user,pull_requests,runs_included,failing_runs", "--jq", "{user, pull_requests, runs_included, run_ids: [.failing_runs[].id]}"}))
	require.JSONEq(t, `{"user":"alice","pull_requests":{"current_branch":null,"authored":[],"assigned":[],"review_requested":[]},"runs_included":true,"run_ids":[7]}`, out.String())
	require.Empty(t, diagnostics.String())
	require.Equal(t, 1, runs)
	actionsEnabled = false
	for _, args := range [][]string{
		{"status", "--json=runs_included,failing_runs"},
		{"status", "-R", "alice/project", "--json=runs_included,failing_runs"},
		{"status", "-R", "alice/project", "--runs=false", "--json=runs_included,failing_runs"},
		{"pr", "status", "-R", "alice/project", "--json"},
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args))
		if args[0] == "pr" {
			require.JSONEq(t, `{"current_branch":null,"authored":[],"assigned":[],"review_requested":[]}`, out.String())
		} else {
			require.JSONEq(t, `{"runs_included":false,"failing_runs":[]}`, out.String())
		}
		require.Equal(t, 1, runs)
	}
}

func TestStatusFailureDoesNotPublishPartialJSON(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("created") == "true" {
			_, _ = w.Write([]byte(`[{"id":1,"number":1,"title":"Authored PR","repository":{"full_name":"alice/project"}}]`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "status", "--json"})
	require.Error(t, err)
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

func TestPullStatusShowsTheCurrentBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/pulls/1":
			_, _ = w.Write([]byte(`{"number":1,"title":"Add one.txt","state":"open","head":{"ref":"feat/one","sha":"abc"},"html_url":"http://gitea/alice/project/pulls/1"}`))
		case r.URL.Query().Get("page") != "" && r.URL.Query().Get("page") != "1":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/pulls/1/reviews":
			_, _ = w.Write([]byte(`[{"id":5,"state":"APPROVED","user":{"login":"bob"}}]`))
		case r.URL.Path == "/api/v1/repos/alice/project/statuses/abc":
			_, _ = w.Write([]byte(`[{"id":1,"context":"ci","status":"pending"},{"id":2,"context":"lint","status":"pending"}]`))
		case r.URL.Query().Get("created_by") == "alice":
			_, _ = w.Write([]byte(`[{"id":1,"number":1,"title":"Add one.txt","repository":{"full_name":"alice/project"},"html_url":"http://gitea/alice/project/pulls/1"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	plainTTY(t, &deps, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1", "GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feat/one", "config --get branch.feat/one.gitea-cli-pr": "1"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "status"}))
	require.Equal(t, `Current branch
  #1  Add one.txt  [feat/one]
    - Checks: 2 pending · Review: Approved

Created by you
  #1  Add one.txt

Assigned to you
  Nothing here

Requesting a code review from you
  Nothing here
`, stripLinks(out.String()))
}

func TestStatusInACloneListsItsRunsWithoutFailingOnAnUnreadableRepository(t *testing.T) {
	readable := true
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project" && readable:
			_, _ = w.Write([]byte(`{"has_actions":true}`))
		case r.URL.Path == "/api/v1/repos/alice/project":
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":7,"display_title":"Broken build","status":"completed","conclusion":"failure"}]}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"config --get gt.repository": server.URL + "/alice/project"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"status", "--json=repository,runs_repository,runs_included"}))
	require.JSONEq(t, `{"repository":"","runs_repository":"alice/project","runs_included":true}`, out.String())
	out.Reset()
	readable = false
	require.NoError(t, Execute(context.Background(), deps, []string{"status", "--json=repository,runs_repository,runs_included"}))
	require.JSONEq(t, `{"repository":"","runs_repository":"","runs_included":false}`, out.String())
	out.Reset()
	require.EqualError(t, Execute(context.Background(), deps, []string{"status", "-R", "alice/project", "--json=runs_included"}), "repository alice/project not found")
}

func TestPullStatusWarnsWhenTheCurrentPullRequestCannotBeRead(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/pulls/1" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"database is down"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	plainTTY(t, &deps, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1", "GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feat/one", "config --get branch.feat/one.gitea-cli-pr": "1"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "status"}))
	require.Contains(t, diagnostics.String(), "database is down")
	require.Contains(t, out.String(), "Created by you")
}
