// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

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
