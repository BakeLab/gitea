// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
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

func TestSearchTables(t *testing.T) {
	var queries []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/issues/search", r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("q") == "nothing" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "7")
		_, _ = w.Write([]byte(`[{"number":14,"title":"Merged","state":"closed","pull_request":{"merged":true},"user":{"login":"bob"},"updated_at":"2026-10-11T18:00:00Z","repository":{"full_name":"alice/project"}},` +
			`{"number":3,"title":"Open","state":"open","user":{"login":"alice"},"updated_at":"2026-10-10T20:00:00Z","repository":{"full_name":"acme/service"}}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) }
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "prs", "login", "-s", "all", "-L", "2", "--author", "@me"}))
	host := strings.TrimPrefix(server.URL, "http://")
	require.Equal(t, "Showing 2 of 7 pull requests matching \"login\" on "+host+" · next: --page 2\n\n"+
		"REPO#ID           TITLE   STATE   AUTHOR  UPDATED\n"+
		"alice/project#14  Merged  merged  bob     about 2 hours ago\n"+
		"acme/service#3    Open    open    alice   about 1 day ago\n", out.String())
	require.Contains(t, queries[0], "created_by=alice")
	require.Contains(t, queries[0], "type=pulls")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "issues", "nothing"}))
	require.Empty(t, out.String())
	require.Equal(t, "no open issues matching \"nothing\" on "+host+"\n", diagnostics.String())
}
