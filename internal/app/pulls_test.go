// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pullListServer lists the pull requests of alice/project: #17 from bob's fork, #15 a draft, #14
// merged and #16 closed, with the total count Gitea reports.
func pullListServer(t *testing.T, states *[]string) *http.Server {
	t.Helper()
	pulls := map[string]string{
		"open": `[{"number":17,"title":"Improve greeting","state":"open","user":{"login":"bob"},"created_at":"2026-10-11T10:00:00Z","head":{"ref":"bob/greeting","repo":{"full_name":"bob/project"}},"html_url":"http://gitea/alice/project/pulls/17"},
			{"number":15,"title":"WIP: experiment","state":"open","draft":true,"user":{"login":"alice"},"created_at":"2026-10-11T09:00:00Z","head":{"ref":"experiment","repo":{"full_name":"alice/project"}}}]`,
		"closed": `[{"number":16,"title":"Add usage guide","state":"closed","user":{"login":"alice"},"head":{"ref":"docs/guide"}},
			{"number":14,"title":"Fix README typo","state":"closed","merged":true,"user":{"login":"alice"},"head":{"ref":"fix/typo"}}]`,
	}
	return &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		*states = append(*states, state)
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "2")
		_, _ = w.Write([]byte(pulls[state]))
	})}
}

func TestPullListTable(t *testing.T) {
	var states []string
	server := newAuthenticatedServer(pullListServer(t, &states).Handler)
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project"}))
	require.Equal(t, "17\tImprove greeting\tbob:bob/greeting\topen\tbob\t2026-10-11T10:00:00Z\n15\tWIP: experiment\texperiment\tdraft\talice\t2026-10-11T09:00:00Z\n", out.String())

	out.Reset()
	plainTTY(t, &deps, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project"}))
	require.Equal(t, `Showing 2 of 2 open pull requests in alice/project

ID   TITLE             BRANCH            AUTHOR  CREATED
#17  Improve greeting  bob:bob/greeting  bob     about 2 hours ago
#15  WIP: experiment   experiment        alice   about 3 hours ago
`, stripLinks(out.String()))

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--state", "merged"}))
	require.Equal(t, "Showing 1 of 1 merged pull requests in alice/project\n\nID   TITLE            BRANCH    STATE   AUTHOR  CREATED\n#14  Fix README typo  fix/typo  merged  alice\n", stripLinks(out.String()))

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "-d"}))
	require.True(t, strings.HasPrefix(stripLinks(out.String()), "Showing 1 of 1 open pull requests in alice/project\n\nID   TITLE            BRANCH      AUTHOR  CREATED\n#15  WIP: experiment"), out.String())

	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "-H", "alice:greeting"}))
	require.Empty(t, out.String())
	require.Equal(t, "no open pull requests match your filters in alice/project\n", diagnostics.String())
	require.Equal(t, []string{"open", "closed", "open"}, slices.Compact(states))
}

func TestPullListFillsReviewersWhenSelected(t *testing.T) {
	views := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/pulls/13" {
			views++
			_, _ = w.Write([]byte(`{"number":13,"requested_reviewers":[{"login":"bob"}]}`))
			return
		}
		_, _ = w.Write([]byte(`[{"number":13,"requested_reviewers":[]}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,draft"}))
	require.JSONEq(t, `[{"number":13,"draft":false}]`, out.String())
	require.Zero(t, views)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "list", "-R", "alice/project", "--json", "number,reviewers"}))
	require.JSONEq(t, `[{"number":13,"reviewers":["bob"]}]`, out.String())
	require.Equal(t, 1, views)
}
