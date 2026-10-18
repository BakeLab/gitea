// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueListSendsTypePageSizeAndFilters(t *testing.T) {
	var queries []url.Values
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/issues", r.URL.Path)
		queries = append(queries, r.URL.Query())
		_, _ = w.Write([]byte(`[{"id":1,"number":2,"title":"Bug","user":{"login":"bob"}}]`))
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	items, more, err := service.List(context.Background(), ListOptions{State: "open", Limit: 1, Page: 3})
	require.NoError(t, err)
	require.False(t, more)
	require.Equal(t, []Issue{{Author: "bob", ID: 1, Number: 2, Kind: KindIssue, Title: "Bug", Labels: []Label{}, Assignees: []string{}}}, items)
	since := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	_, _, err = service.List(context.Background(), ListOptions{
		Kind: "all", Limit: 30, Page: 1, Search: "crash", Assignee: "carol", Labels: "bug,ui", Milestones: "v1",
		Author: "dave", Mentions: "erin", Since: &since,
	})
	require.NoError(t, err)
	require.Equal(t, []url.Values{
		{"state": {"open"}, "type": {"issues"}, "limit": {"1"}, "page": {"3"}},
		{
			"type": {"all"}, "limit": {"30"}, "page": {"1"}, "q": {"crash"}, "assigned_by": {"carol"}, "labels": {"bug,ui"},
			"milestones": {"v1"}, "created_by": {"dave"}, "mentioned_by": {"erin"}, "since": {"2026-10-01T08:00:00Z"},
		},
	}, queries)
}

func TestIssueDependenciesMapReferencesAndTreatMissingSupportAsNone(t *testing.T) {
	missing := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if missing {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, "50", r.URL.Query().Get("limit"))
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/issues/7/dependencies":
			_, _ = w.Write([]byte(`[{"number":3,"repository":{"full_name":"alice/other"}},{"number":0,"repository":null}]`))
		case "/api/v1/repos/alice/project/issues/7/blocks":
			_, _ = w.Write([]byte(`[{"number":9,"repository":{"full_name":"alice/project"}}]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	dependencies, err := service.Dependencies(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, IssueDependencies{
		BlockedBy: []IssueReference{{Repository: "alice/other", Number: 3}, {}},
		Blocks:    []IssueReference{{Repository: "alice/project", Number: 9}},
	}, dependencies)
	missing = true
	dependencies, err = service.Dependencies(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, IssueDependencies{}, dependencies)
}

func TestDiscussionsTellPullRequestsAndMergesApart(t *testing.T) {
	list := `[{"number":1,"state":"open"},{"number":2,"state":"closed","pull_request":{"merged":false}},` +
		`{"number":3,"state":"closed","pull_request":{"merged":true}},{"number":4,"state":"open","pull_request":{"merged":false}}]`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(list))
	}))
	want := []struct{ kind, state string }{{KindIssue, "open"}, {KindPull, "closed"}, {KindPull, "merged"}, {KindPull, "open"}}
	issues, _, err := (Issues{Client: client, Repository: "alice/project"}).List(context.Background(), ListOptions{Kind: "all", State: "all", Limit: 30, Page: 1})
	require.NoError(t, err)
	found, err := (Issues{Client: client, Repository: "alice/project"}).Search(context.Background(), DiscussionSearchOptions{State: "all", Limit: 30, Page: 1})
	require.NoError(t, err)
	require.Len(t, issues, len(want))
	for i, item := range want {
		require.Equal(t, item.kind, issues[i].Kind, issues[i].Number)
		require.Equal(t, item.state, issues[i].State, issues[i].Number)
		require.Equal(t, item.state, found[i].State, found[i].Number)
	}
}

func TestIssueTemplatesAcceptCheckboxOptionObjects(t *testing.T) {
	body := `[{"name":"Form","body":[{"type":"checkboxes","attributes":{"options":[{"label":"I searched","required":true}]}}]}]`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	templates, err := (Issues{Client: client, Repository: "alice/project"}).IssueTemplates(context.Background())
	require.NoError(t, err)
	require.Equal(t, []IssueTemplate{{Name: "Form", Form: true}}, templates)
	var sdkTemplates []*gitea.IssueTemplate
	require.Error(t, json.Unmarshal([]byte(body), &sdkTemplates), "remove the raw request once the SDK decodes checkbox options")
}

func TestIssueCreateInMissingRepositoryReportsTheRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	_, err := (Issues{Client: client, Repository: "alice/projetc"}).Create(context.Background(), IssueOptions{Title: "x"})
	require.EqualError(t, err, "repository alice/projetc not found")
}
