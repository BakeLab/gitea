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

func TestIssueListAuthorMentionsAndTimeBounds(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		query := r.URL.Query()
		require.Equal(t, "alice", query.Get("created_by"))
		require.Equal(t, "bob", query.Get("mentioned_by"))
		require.Equal(t, "2026-10-03T12:00:00Z", query.Get("since"))
		require.Equal(t, "2026-10-04T12:00:00Z", query.Get("before"))
		require.Equal(t, "issues", query.Get("type"))
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--author", "alice", "--mentions", "bob", "--since", "24h", "--until", "0h", "--json"}))
	require.JSONEq(t, `[]`, out.String())
	require.Empty(t, diagnostics.String())
	for _, bounds := range [][]string{{"--since", "2026-10-05", "--until", "2026-10-04"}, {"--since", "invalid"}, {"--until", "-1h"}, {"--since="}} {
		out.Reset()
		args := append([]string{"issue", "list", "-R", "alice/project"}, bounds...)
		err := Execute(context.Background(), deps, args)
		require.Error(t, err)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.Empty(t, out.String())
	}
	require.Equal(t, 1, calls)
}

func TestIssueLabelFiltersRejectUnknownRepositoryLabels(t *testing.T) {
	var listed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/labels":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":1,"name":"bug"},{"id":2,"name":"help wanted"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/issues", "/api/v1/repos/issues/search":
			listed = append(listed, r.URL.Path+"?labels="+r.URL.Query().Get("labels"))
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--label", "Typo"},
		{"issue", "list", "-R", "alice/project", "--label", "bug,Typo"},
		{"search", "issue", "-R", "alice/project", "--label", "Typo"},
		{"search", "pr", "-R", "alice/project", "--label", "Typo"},
	} {
		err := Execute(context.Background(), deps, args)
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, `label "Typo" does not exist`)
	}
	require.Empty(t, listed)
	require.Empty(t, out.String())
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--label", " bug, help wanted"},
		{"search", "issue", "-R", "alice/project", "--label", "bug "},
		{"search", "issue", "--label", "Typo"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	}
	require.Equal(t, []string{
		"/api/v1/repos/alice/project/issues?labels=bug,help wanted",
		"/api/v1/repos/alice/project/issues?labels=bug",
		"/api/v1/repos/issues/search?labels=Typo",
	}, listed)
}

func TestIssueListLinksNumbersOnColorTerminals(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"number":7,"title":"Short","state":"open","user":{"login":"alice"},"html_url":"https://example.org/i/7"},` +
			`{"number":12,"title":"Evil\u001b]52;c;aGk=\u0007\r\nTitle\u202e","state":"closed","user":{"login":"bob"},"html_url":"https://example.org/i/12"}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"issue", "list", "-R", "alice/project"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "7   Short                 open    alice\n12  Evil]52;c;aGk= Title  closed  bob\n", out.String())
	out.Reset()
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "\x1b]8;;https://example.org/i/7\x1b\\7\x1b]8;;\x1b\\   Short                 \x1b[38;2;45;164;78mopen\x1b[0m    alice\n"+
		"\x1b]8;;https://example.org/i/12\x1b\\12\x1b]8;;\x1b\\  Evil]52;c;aGk= Title  \x1b[38;2;207;34;46mclosed\x1b[0m  bob\n", out.String())
}

func TestIssueListKindFilter(t *testing.T) {
	var seen string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		seen = r.URL.Query().Get("type")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "--kind", "pulls", "--json"}))
	require.Equal(t, "pulls", seen)
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"issue", "list", "--kind", "bugs"})))
}

func TestIssueListWithoutRepoPointsToSearch(t *testing.T) {
	deps, out, _ := testDeps(t)
	deps.Git = &fakeGit{}
	err := Execute(context.Background(), deps, []string{"issue", "list", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "tea search issue")
	require.Empty(t, out.String())
}
