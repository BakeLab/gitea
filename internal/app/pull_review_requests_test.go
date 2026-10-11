// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewRequestOutputUsesPullFields(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			require.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/repos/owner/repo/pulls/1":
			require.Equal(t, http.MethodGet, r.Method)
			_, _ = w.Write([]byte(`{"number":1,"requested_reviewers":[{"login":"alice","email":"omitted@example.test"}],"requested_reviewers_teams":[]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "review", "request", "1", "-R", "owner/repo", "--reviewer=alice", "--json=number,reviewers,reviewer_teams"}))
	require.JSONEq(t, `{"number":1,"reviewers":["alice"],"reviewer_teams":[]}`, out.String())
	require.Empty(t, diagnostics.String())
}
