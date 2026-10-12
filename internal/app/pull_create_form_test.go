// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

type recordingPullForm struct {
	calls   int
	values  terminal.PullFormValues
	choices terminal.PullFormChoices
}

func (f *recordingPullForm) CreatePull(_ context.Context, choices terminal.PullFormChoices) (terminal.PullFormValues, error) {
	f.calls++
	f.choices = choices
	return f.values, nil
}

func TestPullCreateImplicitInteractiveForm(t *testing.T) {
	deadline := time.Date(2028, 1, 15, 0, 0, 0, 0, time.UTC)
	var createPayload map[string]any
	server := newAuthenticatedServer(selectablesHandler(t, true, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/raw/.github/pull_request_template.md":
			require.Equal(t, "main", r.URL.Query().Get("ref"))
			_, _ = w.Write([]byte("## Summary\n"))
		case strings.HasPrefix(r.URL.Path, "/api/v1/repos/alice/project/raw/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createPayload))
			_, _ = w.Write([]byte(`{"number":9,"title":"Interactive","html_url":"https://example.org/alice/project/pulls/9","state":"open","base":{"ref":"main"},"head":{"ref":"feature"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	allow := true
	form := &recordingPullForm{values: terminal.PullFormValues{
		IssueFormValues: terminal.IssueFormValues{
			Title: "Interactive", Body: "Body text", Assignees: []string{"alice"}, Milestone: 4, Labels: []int64{2}, Deadline: &deadline,
		},
		Base: "main", Head: "feature", AllowMaintainerEdits: allow,
	}}
	prompt := &scriptedPrompter{accept: true}
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --short HEAD":                                       "feature",
		"rev-parse --is-inside-work-tree":                                 "true",
		"rev-parse --verify refs/heads/feature^{commit}":                  "localsha",
		"for-each-ref --format=%(objectname) refs/remotes/origin/feature": "localsha",
		"remote":                                "origin",
		"remote get-url origin":                 server.URL + "/alice/project.git",
		"config --get branch.feature.remote":    "origin",
		"rev-list --count origin/main..feature": "1",
		"log -1 --format=%s%n%n%b feature":      "Add login\n\nDetails",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--json=number,url", "--interactive"}))
	require.Equal(t, 1, form.calls)
	require.Equal(t, "Add login", form.choices.Title)
	require.Equal(t, "## Summary\n", form.choices.Body)
	require.NotNil(t, form.choices.EditBody)
	require.True(t, form.choices.CanPush)
	require.Equal(t, "main", form.choices.Base)
	require.Equal(t, "feature", form.choices.HeadBranch)
	require.Equal(t, []string{"alice", "bob"}, form.choices.Assignees)
	require.Equal(t, []terminal.IssueMilestoneChoice{{Title: "Release", ID: 4}}, form.choices.Milestones)
	require.Equal(t, []terminal.IssueLabelChoice{{Name: "bug", ID: 2}, {Name: "docs", ID: 3}}, form.choices.Labels)
	require.Empty(t, prompt.confirmations, "submitting the form creates the pull request")
	require.Equal(t, "alice/project", form.choices.Repo)
	require.Equal(t, "Interactive", createPayload["title"])
	require.Equal(t, "Body text", createPayload["body"])
	require.Equal(t, "main", createPayload["base"])
	require.Equal(t, "feature", createPayload["head"])
	require.Equal(t, true, createPayload["allow_maintainer_edit"])
	require.Equal(t, []any{"alice"}, createPayload["assignees"])
	require.Equal(t, float64(4), createPayload["milestone"])
	require.Equal(t, []any{float64(2)}, createPayload["labels"])
	require.Equal(t, "2028-01-15T00:00:00Z", createPayload["due_date"])
	require.JSONEq(t, `{"number":9,"url":"https://example.org/alice/project/pulls/9"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestPullCreateDefaultsFromForkBranchWithoutTTY(t *testing.T) {
	for _, remote := range []string{"fork", "pulls/bob/project"} {
		t.Run(remote, func(t *testing.T) { testPullCreateDefaultsFromForkBranch(t, remote) })
	}
}

func testPullCreateDefaultsFromForkBranch(t *testing.T, remote string) {
	var payload map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			_, _ = w.Write([]byte(`{"number":1,"html_url":"https://example.org/pulls/1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	form := &recordingPullForm{}
	deps, _, _ := serverDeps(t, server.URL)
	deps.PullForm = form
	deps.Git = &fakeGit{results: map[string]string{
		"symbolic-ref --short HEAD":           "über-fix",
		"config --get branch.über-fix.remote": remote,
		"remote get-url " + remote:            server.URL + "/bob/project.git",
	}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "create", "-R", "alice/project", "--no-input"}))
	require.Zero(t, form.calls)
	require.Equal(t, "Über Fix", payload["title"])
	require.Equal(t, "bob:über-fix", payload["head"])
	require.Equal(t, "main", payload["base"])
}
