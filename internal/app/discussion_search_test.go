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

func TestSearchIssueTeamFilter(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/issues/search", r.URL.Path)
		query := r.URL.Query()
		require.Equal(t, "team-org", query.Get("owner"))
		require.Equal(t, "devs", query.Get("team"))
		require.Equal(t, "issues", query.Get("type"))
		_, _ = w.Write([]byte(`[{"id":1,"number":2,"title":"Team issue","html_url":"https://instance/team-org/repo/issues/2","state":"open","repository":{"full_name":"team-org/repo"}}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "issue", "--owner", "team-org", "--team", "devs", "--json=number,title,repository"}))
	require.JSONEq(t, `[{"number":2,"title":"Team issue","repository":"team-org/repo"}]`, out.String())
	require.Empty(t, diagnostics.String())
	require.Equal(t, 1, calls)

	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"search", "issue", "--team", "devs"})))
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"search", "issue", "-R", "team-org/repo", "--owner", "team-org", "--team", "devs"})))
	require.Equal(t, 1, calls)
}

func TestSearchOwnerMatchesRepositoryCaseInsensitively(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/Alice/project/issues", r.URL.Path)
		require.Equal(t, "alice", r.URL.Query().Get("owner"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "issue", "-R", "Alice/project", "--owner", "alice", "--json"}))
	err := Execute(context.Background(), deps, []string{"search", "issue", "-R", "Alice/project", "--owner", "bob", "--json"})
	require.ErrorContains(t, err, "--owner conflicts with selected repository")
}

func TestSearchIssueAcceptsFlexibleTimeBounds(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/issues/search", r.URL.Path)
		query := r.URL.Query()
		require.Equal(t, "2026-10-04T12:00:00Z", query.Get("since"))
		require.Equal(t, "2026-10-05T12:00:00Z", query.Get("before"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "issue", "--since", "24h", "--before", "2026-10-05T12:00:00Z", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"search", "issue", "--since", "invalid"})))
}
