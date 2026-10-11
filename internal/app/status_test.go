// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/tea/internal/clierr"
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
	require.JSONEq(t, `{"user":"alice","pull_requests":{"authored":[],"assigned":[],"review_requested":[]},"runs_included":true,"run_ids":[7]}`, out.String())
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
			require.JSONEq(t, `{"authored":[],"assigned":[],"review_requested":[]}`, out.String())
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
