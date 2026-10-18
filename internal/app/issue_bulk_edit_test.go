// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestIssueBulkEditContinuesAfterIndividualFailure(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		calls = append(calls, r.URL.Path)
		var options services.IssueEdit
		require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
		require.NotNil(t, options.Title)
		require.Equal(t, "Updated", *options.Title)
		if strings.HasSuffix(r.URL.Path, "/2") {
			http.Error(w, `{"message":"permission denied"}`, http.StatusForbidden)
			return
		}
		n := "1"
		if strings.HasSuffix(r.URL.Path, "/3") {
			n = "3"
		}
		_, _ = w.Write([]byte(`{"number":` + n + `,"title":"Updated"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"issue", "edit", "1", "2", "3", "--repo", "alice/project", "--title", "Updated", "--no-input", "--json=number,title"})
	require.ErrorContains(t, err, "issue 2")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.JSONEq(t, `[{"number":1,"title":"Updated"},{"number":3,"title":"Updated"}]`, out.String())
	require.Len(t, calls, 3)
	require.Empty(t, diagnostics.String())
}

func TestIssueEditMergesAssigneesWithOneRead(t *testing.T) {
	reads := 0
	var patched []any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reads++
			_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"},{"login":"bob"}]}`))
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		patched = body["assignees"].([]any)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "7", "-R", "alice/project", "--add-assignee", "carol,carol", "--remove-assignee", "bob", "--json"}))
	require.Equal(t, 1, reads)
	require.Equal(t, []any{"alice", "carol"}, patched)
}

func TestIssueMutationsReportDoneLines(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/5":
			_, _ = w.Write([]byte(`{"number":5,"title":"Open one","state":"open"}`))
		case "GET /api/v1/repos/alice/project/issues/6":
			_, _ = w.Write([]byte(`{"number":6,"title":"Closed one","state":"closed"}`))
		case "GET /api/v1/repos/alice/project/issues/14":
			_, _ = w.Write([]byte(`{"number":14,"title":"Merged one","state":"closed","pull_request":{"merged":true}}`))
		case "POST /api/v1/repos/alice/project/issues":
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"number":9,"title":"New","state":"open","html_url":"https://example.org/alice/project/issues/9"}`))
		case "POST /api/v1/repos/alice/project/issues/5/comments":
			changed = append(changed, r.Method+" "+r.URL.Path+" "+payloadField(t, r, "body"))
			_, _ = w.Write([]byte(`{"id":1}`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"number":5,"title":"Open one","state":"closed"}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	run := func(tty bool, args ...string) {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		deps.Streams.OutTTY = tty
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input")))
	}
	run(true, "issue", "close", "5", "#6", "14", "-c", "Fixed in #7")
	require.Empty(t, out.String())
	require.Equal(t, "✓ Closed issue alice/project#5 (Open one)\n"+
		"! Issue alice/project#6 (Closed one) is already closed\n"+
		"! Pull request alice/project#14 (Merged one) is already merged\n", diagnostics.String())
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/issues/5/comments Fixed in #7", "PATCH /api/v1/repos/alice/project/issues/5"}, changed)
	run(false, "issue", "reopen", "6")
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
	run(true, "issue", "create", "--title", "New")
	require.Equal(t, "https://example.org/alice/project/issues/9\n", out.String())
	require.Equal(t, "✓ Created issue alice/project#9 (New)\n", diagnostics.String())
	run(false, "issue", "new", "--title", "New")
	require.Equal(t, "https://example.org/alice/project/issues/9\n", out.String())
	require.Empty(t, diagnostics.String())
	run(true, "issue", "edit", "5", "--title", "Open one")
	require.Equal(t, "✓ Edited issue alice/project#5 (Open one)\n", diagnostics.String())
}

func TestMissingItemsAreNamed(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "close", "999"}, "issue 999: not found in alice/project"},
		{[]string{"issue", "edit", "999", "--title", "x"}, "issue 999: not found in alice/project"},
		{[]string{"issue", "attachment", "view", "1", "9"}, "attachment 9 not found in alice/project#1"},
		{[]string{"issue", "attachment", "delete", "1", "9", "--yes"}, "attachment 9 not found in alice/project#1"},
	} {
		err := Execute(context.Background(), deps, append(test.args, "-R", "alice/project"))
		require.EqualError(t, Explain(context.Background(), deps, err), test.want, test.args)
	}
}

func TestReopeningAMergedPullRequestLeavesNoComment(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes = append(writes, r.Method+" "+r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"number":14,"title":"Merged one","state":"closed","pull_request":{"merged":true}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"issue", "reopen", "14", "-R", "alice/project", "--no-input", "--comment", "again"})
	require.ErrorContains(t, err, "pull request alice/project#14 (Merged one) is already merged; it cannot be reopened")
	require.Empty(t, writes)
}
