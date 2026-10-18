// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelReplacementClearsAndReportsPartialFailure(t *testing.T) {
	calls := []string{}
	fail := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			var body struct {
				Labels []int64 `json:"labels"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotNil(t, body.Labels)
			require.Empty(t, body.Labels)
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPatch:
			if fail {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var body gitea.EditIssueOption
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, gitea.EditIssueOption{Title: "Updated"}, body)
			_, _ = w.Write([]byte(`{"number":7,"title":"Updated"}`))
		default:
			_, _ = w.Write([]byte(`{"number":7}`))
		}
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	ids := []int64{}
	_, err := service.Edit(context.Background(), 7, IssueEdit{Labels: &ids})
	require.NoError(t, err)
	require.Equal(t, []string{"PUT /api/v1/repos/alice/project/issues/7/labels", "GET /api/v1/repos/alice/project/issues/7"}, calls)
	calls = nil
	title := "Updated"
	_, err = service.Edit(context.Background(), 7, IssueEdit{Labels: &ids, Title: &title})
	require.NoError(t, err)
	require.Len(t, calls, 2)
	fail = true
	_, err = service.Edit(context.Background(), 7, IssueEdit{Labels: &ids, Title: &title})
	require.ErrorContains(t, err, "labels were updated")
}

func TestPullEditMergesChangesWithCurrentState(t *testing.T) {
	var calls []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"labels":[{"id":1},{"id":2}],"assignees":[{"login":"alice"},{"login":"bob"}]}`))
		case "PUT /api/v1/repos/alice/project/issues/7/labels":
			assert.JSONEq(t, `{"labels":[1,3]}`, string(body))
			_, _ = w.Write([]byte(`[]`))
		case "PATCH /api/v1/repos/alice/project/issues/7":
			assertIssueEdit(t, gitea.EditIssueOption{Assignees: []string{"alice", "carol"}}, body)
			_, _ = w.Write([]byte(`{"number":7}`))
		case "GET /api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	changes := MetadataChanges{AddLabels: []int64{3, 1}, RemoveLabels: []int64{2}, AddAssignees: []string{"carol", "Carol", "ALICE"}, RemoveAssignees: []string{"BOB"}}
	_, err := (Pulls{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Changes: &changes})
	require.NoError(t, err)
	require.Equal(t, []string{
		"GET /api/v1/repos/alice/project/issues/7", "PUT /api/v1/repos/alice/project/issues/7/labels",
		"PATCH /api/v1/repos/alice/project/issues/7", "GET /api/v1/repos/alice/project/pulls/7",
	}, calls)
}

func TestIssueEditDeduplicatesAssigneesIgnoringCase(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		assert.Equal(t, "PATCH /api/v1/repos/alice/project/issues/7", r.Method+" "+r.URL.Path)
		assertIssueEdit(t, gitea.EditIssueOption{Assignees: []string{"alice", "bob"}}, body)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	assignees := []string{"alice", "Alice", "bob"}
	_, err := (Issues{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, IssueEdit{Assignees: &assignees})
	require.NoError(t, err)
}

func assertIssueEdit(t *testing.T, want gitea.EditIssueOption, body []byte) {
	t.Helper()
	var got gitea.EditIssueOption
	if assert.NoError(t, json.Unmarshal(body, &got)) {
		assert.Equal(t, want, got)
	}
}

func TestEditAddLabelReplacesTheExclusiveLabelOfTheSameScope(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"labels":[{"id":1,"name":"bug"},{"id":2,"name":"Priority/Low","exclusive":true}]}`))
		case "GET /api/v1/repos/alice/project/labels":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"},{"id":2,"name":"Priority/Low","exclusive":true},{"id":3,"name":"Priority/High","exclusive":true},{"id":4,"name":"Kind/x","exclusive":true}]`))
		case "GET /api/v1/orgs/alice":
			w.WriteHeader(http.StatusNotFound)
		case "PUT /api/v1/repos/alice/project/issues/7/labels":
			assert.JSONEq(t, `{"labels":[1,3]}`, string(body))
			_, _ = w.Write([]byte(`[]`))
		case "GET /api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	_, err := (Issues{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, IssueEdit{Changes: &MetadataChanges{AddLabels: []int64{3}}})
	require.NoError(t, err)
}
