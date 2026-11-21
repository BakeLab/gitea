// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// closedLookupServer has one open pull request, for feat, and closed ones for gone, listed
// newest first like sort=recentupdate.
func closedLookupServer(t *testing.T, requests *[]string) string {
	t.Helper()
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = append(*requests, r.Method+" "+r.URL.Path+"?state="+r.URL.Query().Get("state"))
		if number, ok := strings.CutPrefix(r.URL.Path, "/api/v1/repos/alice/project/pulls/"); ok {
			_, _ = fmt.Fprintf(w, `{"number":%s,"title":"Old","state":"closed","merged":true,"head":{"ref":"gone","sha":"abc","repo":{"full_name":"alice/project"}}}`, number)
			return
		}
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
			return
		}
		if r.URL.Path != "/api/v1/repos/alice/project/pulls" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") == "open" {
			writeFirstPage(w, r, `[{"number":3,"head":{"ref":"feat","repo":{"full_name":"alice/project"}}}]`)
			return
		}
		require.Equal(t, "closed", r.URL.Query().Get("state"))
		require.Equal(t, "recentupdate", r.URL.Query().Get("sort"))
		writeFirstPage(w, r, `[
			{"number":40,"head":{"ref":"gone","repo":{"full_name":"bob/project"}}},
			{"number":31,"head":{"ref":"gone","repo":{"full_name":"alice/project"}}},
			{"number":30,"head":{"ref":"gone","repo":{"full_name":"alice/project"}}}
		]`)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestPullSelectorAnyFallsBackToTheNewestClosedPull(t *testing.T) {
	var requests []string
	serverURL := closedLookupServer(t, &requests)
	for _, tc := range []struct {
		name string
		arg  string
		want int64
		err  string
	}{
		{"open one wins", "feat", 3, ""},
		{"newest closed", "gone", 40, ""},
		{"owner qualified", "alice:gone", 31, ""},
		{"unknown", "nothing", 0, `no open pull request for branch "nothing"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := refApplicationWith(t, serverURL, nil)
			_, n, err := a.pullSelectorAny(commandContext(), []string{tc.arg})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, n)
		})
	}
	// Commands that need an open pull request still do not see closed ones.
	requests = nil
	a, _ := refApplicationWith(t, serverURL, nil)
	_, _, err := a.pullSelector(commandContext(), []string{"gone"})
	require.ErrorContains(t, err, `no open pull request for branch "gone"`)
	require.NotContains(t, strings.Join(requests, "\n"), "state=closed")
}

func TestPullSelectorAnyFallsBackForTheCheckedOutBranch(t *testing.T) {
	var requests []string
	serverURL := closedLookupServer(t, &requests)
	a, _ := refApplicationWith(t, serverURL, map[string]string{"symbolic-ref --quiet HEAD": "gone"})
	// The fork's newer pull request for a branch named like the local one is not this repository's.
	_, n, err := a.pullSelectorAny(commandContext(), nil)
	require.NoError(t, err)
	require.Equal(t, int64(31), n)
	_, _, err = a.pullSelector(commandContext(), nil)
	require.ErrorContains(t, err, `no open pull request for branch "gone"`)
}

func TestPullCommandsFindClosedPullsByBranch(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		err  string
		// request is the request that proves the closed pull request was found.
		request string
	}{
		{"view", []string{"pr", "view", "alice:gone", "--json=number"}, "", ""},
		{"checks", []string{"pr", "checks", "alice:gone", "--web", "--no-input"}, "", ""},
		{"reopen", []string{"pr", "reopen", "alice:gone"}, "already merged", ""},
		{"clean", []string{"pr", "clean", "alice:gone", "--no-input"}, "", ""},
		{"edit", []string{"pr", "edit", "alice:gone", "--title", "New"}, "", "PATCH /api/v1/repos/alice/project/pulls/31?state="},
		{"checkout", []string{"pr", "checkout", "alice:gone", "--detach"}, "missing fake", "GET /api/v1/repos/alice/project/pulls?state=closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			serverURL := closedLookupServer(t, &requests)
			deps, _, _ := serverDeps(t, serverURL)
			deps.Getenv = serverEnv(serverURL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
			deps.Git = &fakeGit{results: map[string]string{"rev-parse --is-inside-work-tree": "true"}}
			err := Execute(context.Background(), deps, tc.args)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
			}
			if err != nil {
				require.NotContains(t, err.Error(), "no open pull request")
			}
			request := cmp.Or(tc.request, "GET /api/v1/repos/alice/project/pulls/31?state=")
			require.Contains(t, requests, request, "err: %v", err)
		})
	}
}

func TestPullMergeKeepsOpenOnlyLookup(t *testing.T) {
	var requests []string
	serverURL := closedLookupServer(t, &requests)
	deps, _, _ := serverDeps(t, serverURL)
	deps.Getenv = serverEnv(serverURL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{}}
	err := Execute(context.Background(), deps, []string{"pr", "merge", "gone", "--yes", "--no-input"})
	require.ErrorContains(t, err, `no open pull request for branch "gone"`)
	require.NotContains(t, strings.Join(requests, "\n"), "state=closed")
}
