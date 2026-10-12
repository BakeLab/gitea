// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type recordingIssueForm struct {
	calls   int
	values  terminal.IssueFormValues
	choices terminal.IssueFormChoices
}

func (f *recordingIssueForm) CreateIssue(_ context.Context, choices terminal.IssueFormChoices) (terminal.IssueFormValues, error) {
	f.calls++
	f.choices = choices
	return f.values, nil
}

func TestIssueCreateImplicitInteractiveForm(t *testing.T) {
	deadline := time.Date(2028, 1, 15, 0, 0, 0, 0, time.UTC)
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/alice/project/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
		_, _ = w.Write([]byte(`{"number":9,"title":"Interactive","html_url":"https://example.org/alice/project/issues/9","state":"open"}`))
	}))
	defer server.Close()
	form := &recordingIssueForm{values: terminal.IssueFormValues{
		Title: "Interactive", Body: "Body text", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, Deadline: &deadline,
	}}
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--json=number,url", "--interactive"}))
	require.Equal(t, 1, form.calls)
	require.NotNil(t, form.choices.EditBody)
	require.True(t, form.choices.CanPush)
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees)
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Equal(t, []terminal.IssueTemplateChoice{{Name: "Bug", About: "Report", Title: "bug: ", Body: "## Steps"}}, form.choices.Templates)
	require.Len(t, prompt.confirmations, 1)
	require.Equal(t, "Interactive", createPayload["title"])
	require.Equal(t, "Body text", createPayload["body"])
	require.Equal(t, []any{"alice"}, createPayload["assignees"])
	require.Equal(t, float64(4), createPayload["milestone"])
	require.Equal(t, []any{float64(2)}, createPayload["labels"])
	require.Equal(t, "2028-01-15T00:00:00Z", createPayload["due_date"])
	require.JSONEq(t, `{"number":9,"url":"https://example.org/alice/project/issues/9"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestIssueCreateInteractiveDraftSurvivesSubmissionFailure(t *testing.T) {
	server := newAuthenticatedServer(selectablesHandler(t, false, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/alice/project/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, path := newDraftEditor(t, "saved body")
	form := &recordingIssueForm{values: terminal.IssueFormValues{
		Title: "Interactive", Body: "saved body", Draft: terminal.Draft{Path: path, Body: "saved body"},
	}}
	deps, out, _ := serverDeps(t, server.URL)
	deps.IssueForm = form
	deps.Prompter = &scriptedPrompter{accept: true}
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	err := Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--interactive"})
	require.ErrorContains(t, err, "draft saved at "+path)
	require.NotNil(t, form.choices.EditBody)
	requireDraft(t, path, true)
	require.Empty(t, out.String())
}

func TestIssueCreateSkipsInteractiveFormWithoutTTY(t *testing.T) {
	form := &recordingIssueForm{}
	deps, _, _ := testDeps(t)
	deps.IssueForm = form
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	err := Execute(context.Background(), deps, []string{"issue", "create", "-R", "alice/project", "--no-input"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "supply --title")
	require.Zero(t, form.calls)
}
