// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
			_, _ = w.Write([]byte(`{"id":5,"number":5,"title":"Edited","body":"New body","html_url":"https://example.org/issues/5","state":"open",` +
				`"assignees":[{"login":"bob"}],"milestone":{"id":4},"due_date":"2028-02-01T23:59:59Z"}`))
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
	require.InDelta(t, float64(4), patchPayload["milestone"], 0)
	require.Equal(t, "2028-02-01T23:59:59Z", patchPayload["due_date"], "a date is the end of that day, as with --deadline")
	require.Equal(t, []any{float64(3)}, labelPayload["labels"])
	require.JSONEq(t, `{"number":5,"title":"Edited"}`, out.String())
	require.Empty(t, diagnostics.String())

	// A form left as it was writes nothing, and one change writes only that field.
	for _, tc := range []struct {
		edit    func(*terminal.IssueEditFormValues)
		want    map[string]any
		warning string
	}{
		{func(*terminal.IssueEditFormValues) {}, nil, "No changes to issue alice/project#5 (Old)"},
		{func(v *terminal.IssueEditFormValues) { v.Title = "Renamed" }, map[string]any{"title": "Renamed"}, ""},
	} {
		patchPayload, labelPayload = nil, nil
		diagnostics.Reset()
		deps.IssueEditForm = issueEditFormFunc(func(_ context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
			labels := make([]int64, len(choices.Labels))
			for i, label := range choices.Labels {
				labels[i] = label.ID
			}
			due, err := time.Parse(time.DateOnly, choices.Deadline.Format(time.DateOnly))
			require.NoError(t, err)
			values := terminal.IssueEditFormValues{
				Title: choices.Title, Body: choices.Body, Assignees: []string{"Carol", "alice"}, Milestone: choices.Milestone.ID, Labels: labels, Deadline: &due,
			}
			tc.edit(&values)
			return values, nil
		})
		require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "5", "-R", "alice/project", "--interactive"}))
		if tc.warning == "" {
			require.Empty(t, diagnostics.String())
		} else {
			require.Contains(t, diagnostics.String(), tc.warning)
		}
		maps.DeleteFunc(patchPayload, func(_ string, value any) bool { return value == nil }) // null leaves a field as it is
		require.Equal(t, tc.want, patchPayload)
		require.Nil(t, labelPayload, "unchanged labels are not replaced")
	}
}

// issueAuthorServer answers an edit of issue 5 like Gitea does for its author who cannot write to
// the issues: the title changes, the milestone, assignees and due date stay as they were, and a
// label change is refused.
func issueAuthorServer(t *testing.T, patches *[]map[string]any) *httptest.Server {
	const current = `{"id":5,"number":5,"title":"%s","state":"open","assignees":[{"login":"alice"}],"milestone":{"id":9,"title":"Shipped"},"due_date":"2028-01-15T23:59:59Z"}`
	form := selectablesHandler(t, false, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project/issues/5":
			_, _ = fmt.Fprintf(w, current, "Old")
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/alice/project/issues/5":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			*patches = append(*patches, payload)
			_, _ = fmt.Fprintf(w, current, payload["title"])
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/repos/alice/project/issues/5/labels":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"write permission is required"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// --milestone looks the milestone up among all of them, where the form offers the open ones.
		if r.URL.Path == "/api/v1/repos/alice/project/milestones" && r.URL.Query().Get("state") == "all" {
			writeFirstPage(w, r, `[{"id":4,"title":"Release","state":"open"}]`)
			return
		}
		form(w, r)
	}))
}

func TestIssueEditFormOffersMetadataWithoutPushAndNamesWhatGiteaLeftOut(t *testing.T) {
	var patches []map[string]any
	server := issueAuthorServer(t, &patches)
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.IssueEditForm = issueEditFormFunc(func(_ context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
		require.Equal(t, []string{"alice", "bob"}, choices.AvailableAssignees, "the form offers metadata without push access")
		require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, choices.Milestones)
		require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, choices.AvailableLabels)
		due := time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC)
		return terminal.IssueEditFormValues{Title: "Edited", Body: choices.Body, Assignees: []string{"bob"}, Milestone: 4, Deadline: &due}, nil
	})
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"issue", "edit", "5", "-R", "alice/project", "--json=number,title", "--interactive"})
	require.EqualError(t, err, "issue #5 was edited without milestone, assignees, due date: Gitea leaves them out for people who cannot write to the issues of alice/project")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Len(t, patches, 1)
	require.Equal(t, []any{"bob"}, patches[0]["assignees"])
	require.InDelta(t, float64(4), patches[0]["milestone"], 0)
	require.JSONEq(t, `{"number":5,"title":"Edited"}`, out.String(), "the edited issue is still reported")
}

func TestIssueEditFlagsNameWhatGiteaLeftOut(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--add-assignee", "bob", "--milestone", "Release", "--deadline", "2028-02-01"}, "--milestone, --add-assignee, --deadline"},
		{[]string{"--remove-assignee", "alice", "--remove-milestone", "--deadline="}, "--remove-milestone, --remove-assignee, --deadline"},
		{[]string{"--set-assignees", "bob", "--milestone", "0"}, "--milestone, --set-assignees"},
		{[]string{"--milestone", "Release"}, "--milestone"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var patches []map[string]any
			server := issueAuthorServer(t, &patches)
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append([]string{"issue", "edit", "5", "-R", "alice/project", "--title", "Edited", "--json=number,title"}, tc.args...))
			them := "them"
			if !strings.Contains(tc.want, ",") {
				them = "it"
			}
			require.EqualError(t, err, "issue #5 was edited without "+tc.want+": Gitea leaves "+them+" out for people who cannot write to the issues of alice/project")
			require.Equal(t, clierr.Failure, clierr.ExitCode(err))
			require.Len(t, patches, 1, "no push access is required before editing")
			require.Equal(t, "Edited", patches[0]["title"])
			require.JSONEq(t, `{"number":5,"title":"Edited"}`, out.String(), "the edited issue is still reported")
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestIssueEditSavesTheOtherFieldsWhenGiteaRefusesLabels(t *testing.T) {
	const refused = "issue 5: the other changes were sent, but the labels were left unchanged: Gitea refuses label changes from people who cannot write to the issues of alice/project"
	t.Run("flags", func(t *testing.T) {
		var patches []map[string]any
		server := issueAuthorServer(t, &patches)
		defer server.Close()
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, []string{"issue", "edit", "5", "-R", "alice/project", "--title", "Edited", "--add-label", "bug", "--milestone", "Release"})
		require.EqualError(t, err, refused+"\nissue #5 was edited without --milestone: Gitea leaves it out for people who cannot write to the issues of alice/project")
		require.Len(t, patches, 1)
		require.Equal(t, "Edited", patches[0]["title"])
	})
	t.Run("form", func(t *testing.T) {
		var patches []map[string]any
		server := issueAuthorServer(t, &patches)
		defer server.Close()
		deps, _, _ := serverDeps(t, server.URL)
		deps.IssueEditForm = issueEditFormFunc(func(_ context.Context, choices terminal.IssueEditFormChoices) (terminal.IssueEditFormValues, error) {
			return terminal.IssueEditFormValues{Title: "Edited", Body: choices.Body, Assignees: choices.Assignees, Milestone: 9, Labels: []int64{2}}, nil
		})
		deps.Prompter = &scriptedPrompter{accept: true}
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		err := Execute(context.Background(), deps, []string{"issue", "edit", "5", "-R", "alice/project", "--interactive"})
		require.EqualError(t, err, refused)
		require.Len(t, patches, 1)
		require.Equal(t, "Edited", patches[0]["title"])
	})
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
		if r.Method == http.MethodGet && r.URL.Path != "/api/v1/repos/alice/project/issues/7" {
			writeFirstPage(w, r, `[]`) // the dependencies, which --json includes
			return
		}
		if r.Method == http.MethodGet {
			reads++
			_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"},{"login":"bob"}]}`))
			return
		}
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		var ok bool
		patched, ok = body["assignees"].([]any)
		require.True(t, ok)
		_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"},{"login":"carol"}]}`))
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

func TestMissingRepositoryIsNamedOnIssueWrites(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "close", "1", "2"}, "issue 1: repository alice/typo not found\nissue 2: repository alice/typo not found"},
		{[]string{"issue", "reopen", "1"}, "issue 1: repository alice/typo not found"},
		{[]string{"issue", "edit", "999", "--title", "x"}, "issue 999: repository alice/typo not found"},
		{[]string{"milestone", "issue", "add", "7", "999"}, "repository alice/typo not found"},
	} {
		err := Execute(context.Background(), deps, append(test.args, "-R", "alice/typo"))
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

func TestRefusedStateChangeRemovesItsComment(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes = append(writes, r.Method+" "+r.URL.Path)
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/project/issues/5/comments":
			_, _ = w.Write([]byte(`{"id":31}`))
		case "PATCH /api/v1/repos/alice/project/issues/5", "PATCH /api/v1/repos/alice/project/pulls/5":
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"message":"cannot close this issue because it still has open dependencies"}`))
		case "DELETE /api/v1/repos/alice/project/issues/comments/31":
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`{"number":5,"title":"Open one","state":"open"}`))
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{{"issue", "close", "5"}, {"pr", "close", "5"}} {
		writes = nil
		require.Error(t, Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input", "-c", "Done in #14")), args)
		require.Equal(t, "DELETE /api/v1/repos/alice/project/issues/comments/31", writes[len(writes)-1], args)
	}
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
	require.Equal(t, []string{"DELETE alice/project#4", "POST alice/project#9", "POST alice/project#3", "POST bob/lib#5"}, requests)
	requireUsageBeforeRequests(t, "takes issues as N, #N, OWNER/REPO#N or an issue URL", "issue", "edit", "12", "--add-blocked-by", "lib#5")
	requests = nil
	for _, refused := range []struct{ want, flag, value string }{
		{"alice/project#9 is both added and removed", "--remove-blocked-by", "alice/project#9"},
		{"--add-blocked-by alice/project#12 names the edited issue; an issue cannot block itself", "--add-blocked-by", "alice/project#12"},
		{"--add-blocked-by https://elsewhere.example/bob/lib/issues/5 is on https://elsewhere.example, not on " + server.URL, "--add-blocked-by", "https://elsewhere.example/bob/lib/issues/5"},
	} {
		err := Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", "9", refused.flag, refused.value})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, refused.want)
	}
	require.Empty(t, requests)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", server.URL + "/bob/lib/issues/5"}))
	require.Equal(t, []string{"POST bob/lib#5"}, requests, "an issue URL of the instance names its repository")
}

func TestIssueEditOutputsDependenciesAsJSON(t *testing.T) {
	var reads atomic.Int32 // the two lists are read concurrently
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/project/issues/12/dependencies":
			_, _ = w.Write([]byte(`{"number":12}`))
		case "GET /api/v1/repos/alice/project/issues/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Crash"}`))
		case "GET /api/v1/repos/alice/project/issues/12/dependencies":
			reads.Add(1)
			writeFirstPage(w, r, `[{"number":9,"repository":{"full_name":"alice/project"}}]`)
		case "GET /api/v1/repos/alice/project/issues/12/blocks":
			reads.Add(1)
			writeFirstPage(w, r, `[]`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", "9", "--json", "number"}))
	require.JSONEq(t, `{"number":12}`, out.String())
	require.Zero(t, reads.Load(), "dependencies are read only when the output includes them")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", "9", "--json", "number,blocked_by"}))
	require.JSONEq(t, `{"number":12,"blocked_by":[{"repository":"alice/project","number":9}]}`, out.String())
}

func TestIssueEditExplainsExistingAndMissingDependencies(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/project/issues/12/dependencies":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"issue dependency does already exist [issue id: 115, dependency id: 116]"}`))
		case "DELETE /api/v1/repos/alice/project/issues/12/dependencies":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"issue dependency does not exist [issue id: 115, dependency id: 116]"}`))
		case "POST /api/v1/repos/alice/project/issues/13/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 7, index: 999]"}`))
		case "POST /api/v1/repos/alice/project/issues/14/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 7, index: 14]"}`))
		case "POST /api/v1/repos/alice/project/issues/15/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"repository does not exist [id: 0, uid: 0, owner_name: bob, name: nope]"}`))
		case "POST /api/v1/repos/alice/project/issues/16/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 8, index: 16]"}`))
		case "GET /api/v1/repos/alice/project/issues/12":
			_, _ = w.Write([]byte(`{"number":12}`))
		case "GET /api/v1/repos/alice/project/issues/16":
			_, _ = w.Write([]byte(`{"number":16}`))
		case "POST /api/v1/repos/alice/project/issues/17/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 7, index: 17]"}`))
		case "GET /api/v1/repos/alice/project/issues/17":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "POST /api/v1/repos/alice/project/issues/18/dependencies", "POST /api/v1/repos/alice/nodeps/issues/18/dependencies":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "GET /api/v1/repos/alice/project/issues/18", "GET /api/v1/repos/alice/nodeps/issues/18":
			_, _ = w.Write([]byte(`{"number":18}`))
		case "GET /api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		case "GET /api/v1/repos/alice/nodeps":
			_, _ = w.Write([]byte(`{"full_name":"alice/nodeps","internal_tracker":{"enable_issue_dependencies":false}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	// Adding an existing dependency or removing a missing one changes nothing, like other additions.
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--add-blocked-by", "9"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "alice/project", "--remove-blocked-by", "bob/lib#9"}))
	// A 404 names the missing blocker, its repository, or the edited issue.
	err := Execute(context.Background(), deps, []string{"issue", "edit", "13", "-R", "alice/project", "--add-blocked-by", "999"})
	require.EqualError(t, err, "issue 13: issue alice/project#999 not found")
	err = Execute(context.Background(), deps, []string{"issue", "edit", "14", "-R", "alice/project", "--add-blocked-by", "999"})
	require.EqualError(t, err, "issue 14: not found in alice/project")
	err = Execute(context.Background(), deps, []string{"issue", "edit", "15", "-R", "alice/project", "--add-blocked-by", "bob/nope#1"})
	require.EqualError(t, err, "issue 15: repository bob/nope not found")
	// A blocker in another repository with the edited issue's number: the edited issue exists.
	err = Execute(context.Background(), deps, []string{"issue", "edit", "16", "-R", "alice/project", "--add-blocked-by", "bob/lib#16"})
	require.EqualError(t, err, "issue 16: issue bob/lib#16 not found")
	err = Execute(context.Background(), deps, []string{"issue", "edit", "17", "-R", "alice/project", "--add-blocked-by", "bob/lib#17"})
	require.EqualError(t, err, "issue 17: not found in alice/project")
	// A bare 404 for an existing issue means no write access, or dependencies turned off.
	err = Execute(context.Background(), deps, []string{"issue", "edit", "18", "-R", "alice/project", "--add-blocked-by", "1"})
	require.EqualError(t, err, "issue 18: changing the dependencies of issue #18 requires write access to the issues of alice/project")
	err = Execute(context.Background(), deps, []string{"issue", "edit", "18", "-R", "alice/nodeps", "--add-blocked-by", "1"})
	require.EqualError(t, err, "issue 18: issue dependencies are disabled in alice/nodeps")
}
