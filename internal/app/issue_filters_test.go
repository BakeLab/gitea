// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"github.com/spf13/cobra"
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

func TestIssueListTables(t *testing.T) {
	var queries []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("created_by") == "carol" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "12")
		_, _ = w.Write([]byte(`[{"number":7,"title":"Short","state":"open","user":{"login":"alice"},"html_url":"https://example.org/i/7",` +
			`"labels":[{"name":"bug"},{"name":"priority/high"}],"updated_at":"2026-10-11T18:00:00Z"},` +
			`{"number":12,"title":"Evil\u001b]52;c;aGk=\u0007\r\nTitle\u202e","state":"closed","user":{"login":"bob"},"html_url":"https://example.org/i/12","updated_at":"2026-10-09T20:00:00Z"},` +
			`{"number":14,"title":"Merged","state":"closed","pull_request":{"merged":true},"user":{"login":"bob"},"html_url":"https://example.org/p/14","updated_at":"2026-10-11T19:59:00Z"}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	args := []string{"issue", "list", "-R", "alice/project", "-L", "3"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Equal(t, "7\tShort\tbug, priority/high\topen\tissue\talice\t2026-10-11T18:00:00Z\n"+
		"12\tEvil]52;c;aGk= Title\t\tclosed\tissue\tbob\t2026-10-09T20:00:00Z\n"+
		"14\tMerged\t\tmerged\tpr\tbob\t2026-10-11T19:59:00Z\n", out.String())
	for _, test := range []struct {
		args []string
		want string
	}{
		{nil, "Showing 3 of 12 open issues in alice/project · next: --page 2\n\n" +
			"ID   TITLE                 LABELS              AUTHOR  UPDATED\n" +
			"#7   Short                 bug, priority/high  alice   about 2 hours ago\n" +
			"#12  Evil]52;c;aGk= Title                      bob     about 2 days ago\n" +
			"#14  Merged                                    bob     about 1 minute ago\n"},
		{[]string{"--state", "all", "--kind", "all"}, "Showing 3 of 12 issues and pull requests in alice/project · next: --page 2\n\n" +
			"ID   TITLE                 LABELS              STATE   KIND   AUTHOR  UPDATED\n" +
			"#7   Short                 bug, priority/high  open    issue  alice   about 2 hours ago\n" +
			"#12  Evil]52;c;aGk= Title                      closed  issue  bob     about 2 days ago\n" +
			"#14  Merged                                    merged  pr     bob     about 1 minute ago\n"},
	} {
		out.Reset()
		deps.Streams.OutTTY = true
		require.NoError(t, Execute(context.Background(), deps, append(append(args, test.args...), "--no-input")))
		require.Equal(t, test.want, stripLinks(withoutANSI(out.String())))
	}
	require.Contains(t, out.String(), "\x1b]8;;https://example.org/i/7\x1b\\\x1b[32m#7\x1b[0m\x1b]8;;\x1b\\")
	require.Contains(t, out.String(), "\x1b[35mmerged\x1b[0m")
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--author", "carol")))
	require.Empty(t, out.String())
	require.Equal(t, "no open issues match your filters in alice/project\n", diagnostics.String())
	queries = nil
	require.NoError(t, Execute(context.Background(), deps, append(args, "-A", "@me", "-a", "@me", "--json")))
	require.Contains(t, queries[0], "assigned_by=alice")
	require.Contains(t, queries[0], "created_by=alice")
}

func TestIssueListWebOpensTheFilteredPage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	browser := &recordingBrowser{}
	deps.Browser = browser
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project", "-w", "-s", "closed", "-S", "login bug"}))
	require.Equal(t, server.URL+"/alice/project/issues?q=login+bug&state=closed", browser.resource)
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
	require.ErrorContains(t, err, "gt search issue")
	require.Empty(t, out.String())
}

func TestIssueMilestoneFiltersRejectUnknownMilestones(t *testing.T) {
	var listed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/milestones":
			require.Equal(t, "all", r.URL.Query().Get("state"))
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":1,"title":"v1.0"},{"id":2,"title":"2026","state":"closed"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "/api/v1/repos/alice/project/issues", "/api/v1/repos/issues/search":
			listed = append(listed, r.URL.Path+"?milestones="+r.URL.Query().Get("milestones"))
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "list", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
		{[]string{"issue", "list", "--milestone", "999"}, "milestone 999 not found in alice/project"},
		{[]string{"issue", "list", "--milestone", "v1.0,nope"}, `milestone "nope" not found`},
		{[]string{"search", "issue", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
		{[]string{"search", "pr", "--milestone", "nope"}, `milestone "nope" not found in alice/project`},
	} {
		err := Execute(context.Background(), deps, append(test.args, "-R", "alice/project", "--json"))
		require.Equal(t, clierr.Failure, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, test.want)
	}
	require.Empty(t, listed)
	require.Empty(t, out.String())
	for _, args := range [][]string{
		{"issue", "list", "-R", "alice/project", "--milestone", "v1.0,2026"},
		{"issue", "list", "-R", "alice/project", "--milestone", "1"},
		{"search", "issue", "-R", "alice/project", "--milestone", "V1.0"},
		{"search", "issue", "--milestone", "nope"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "--json")))
	}
	require.Equal(t, []string{
		"/api/v1/repos/alice/project/issues?milestones=1,2",
		"/api/v1/repos/alice/project/issues?milestones=1",
		"/api/v1/repos/alice/project/issues?milestones=1",
		"/api/v1/repos/issues/search?milestones=nope",
	}, listed)
	require.Contains(t, diagnostics.String(), "pass --repo to check them")
}

func TestIssueCommandsComplete(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues":
			_, _ = w.Write([]byte(`[{"number":4,"title":"Crash","state":` + strconv.Quote(r.URL.Query().Get("state")) + `}]`))
		case "/api/v1/repos/alice/project/labels":
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
		case "/api/v1/repos/alice/project/milestones":
			_, _ = w.Write([]byte(`[{"id":3,"title":"v1.0"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"issue", "list", "--state", ""}, []string{"open", "closed", "all"}},
		{[]string{"issue", "list", "-s", "c"}, []string{"closed"}},
		{[]string{"issue", "list", "--kind", ""}, []string{"issues", "pulls", "all"}},
		{[]string{"issue", "list", "--label", ""}, []string{"bug"}},
		{[]string{"issue", "list", "--milestone", "v1.0,"}, []string{}},
		{[]string{"issue", "create", "-m", ""}, []string{"v1.0"}},
		{[]string{"issue", "edit", "4", "--add-label", ""}, []string{"bug"}},
		{[]string{"issue", "view", ""}, []string{"4\tCrash"}},
		{[]string{"issue", "view", "4", ""}, []string{}},
		{[]string{"issue", "close", "5", ""}, []string{"4\tCrash"}},
		{[]string{"issue", "reopen", ""}, []string{"4\tCrash"}},
	} {
		values, directive := complete(t, deps, out, test.args...)
		require.Equal(t, test.want, values, test.args)
		require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive&cobra.ShellCompDirectiveNoFileComp, test.args)
	}
}
