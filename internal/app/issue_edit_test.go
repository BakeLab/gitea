// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type issueEditFormFunc func(context.Context, terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error)

func (f issueEditFormFunc) EditIssue(ctx context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
	return f(ctx, choices)
}

func TestIssueEditImplicitInteractiveForm(t *testing.T) {
	deadline := "2028-01-15T00:00:00Z"
	var patchPayload map[string]any
	var labelPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/5":
			_, _ = w.Write([]byte(`{"id":5,"number":5,"title":"Old","body":"Old body","html_url":"https://example.org/issues/5","state":"open","assignees":[{"login":"alice"},{"login":"carol"}],"labels":[{"id":2,"name":"bug"},{"id":7,"name":"org"}],"milestone":{"id":9,"title":"Shipped"},"due_date":"` + deadline + `"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/repos/alice/project/issues/5/labels":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&labelPayload))
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/issues/5":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&patchPayload))
			_, _ = w.Write([]byte(`{"id":5,"number":5,"title":"Edited","body":"New body","html_url":"https://example.org/issues/5","state":"open"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	calls := 0
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.IssueEditForm = issueEditFormFunc(func(_ context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
		require.NotNil(t, choices.EditBody)
		require.Equal(t, "Editing alice/project#5 (Old)", choices.Heading)
		calls++
		require.Equal(t, "Old", choices.Title)
		require.Equal(t, "Old body", choices.Body)
		require.Equal(t, []string{"alice", "carol"}, choices.Assignees)
		require.Equal(t, &terminal.IssueMilestoneChoice{Title: "Shipped", ID: 9}, choices.Milestone)
		require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "org", ID: 7}}, choices.Labels)
		require.NotNil(t, choices.Deadline)
		require.True(t, choices.CanPush)
		require.Equal(t, []string{"alice", "bob"}, choices.AvailableAssignees)
		due := time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC)
		return terminal.IssueEditFormValues{
			Title: "Edited", Body: "New body", Assignees: []string{"bob"}, Milestone: 4, Labels: []int64{3}, Deadline: &due,
		}, nil
	})
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "5", "-R", "alice/project", "--json=number,title", "--interactive"}))
	require.Equal(t, 1, calls)
	require.Empty(t, prompt.confirmations)
	require.Equal(t, "Edited", patchPayload["title"])
	require.Equal(t, "New body", patchPayload["body"])
	require.Equal(t, []any{"bob"}, patchPayload["assignees"])
	require.Equal(t, float64(4), patchPayload["milestone"])
	require.Equal(t, []any{float64(3)}, labelPayload["labels"])
	require.JSONEq(t, `{"number":5,"title":"Edited"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestIssueEditAddAndRemoveLabels(t *testing.T) {
	puts := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/alice":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v1/repos/alice/project/labels":
			writeFirstPage(w, r, `[{"id":1,"name":"bug"},{"id":2,"name":"docs"},{"id":3,"name":"help"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"labels":[{"id":1,"name":"bug"},{"id":2,"name":"docs"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/repos/alice/project/issues/7/labels":
			puts++
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"labels":[1,3]}`, string(body))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"},{"id":3,"name":"help"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "7", "-R", "alice/project", "--add-label", "help", "--remove-label", "docs", "--json=number,labels"}))
	require.Equal(t, 1, puts)
	require.Contains(t, out.String(), `"number":7`)
}

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

func TestIssueEditAddsAndRemovesDependencies(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/project/issues/12/dependencies", "DELETE /api/v1/repos/alice/project/issues/12/dependencies":
			var meta map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&meta))
			requests = append(requests, fmt.Sprintf("%s %v/%v#%v", r.Method, meta["owner"], meta["repo"], meta["index"]))
			_, _ = w.Write([]byte(`{"number":12}`))
		case "GET /api/v1/repos/alice/project/issues/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Crash"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", "#9,3,bob/lib#5", "--remove-blocked-by", "4"}))
	require.Equal(t, []string{"POST alice/project#9", "POST alice/project#3", "POST bob/lib#5", "DELETE alice/project#4"}, requests)
	requireUsageBeforeRequests(t, "takes issues as N, #N or OWNER/REPO#N", "issue", "edit", "12", "--add-blocked-by", "lib#5")
	requireUsageBeforeRequests(t, "#9 is both added and removed", "issue", "edit", "12", "--add-blocked-by", "9", "--remove-blocked-by", "9")
}
