// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelReplacementClearsAfterTheOtherFields(t *testing.T) {
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
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			var body gitea.EditIssueOption
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, gitea.EditIssueOption{Title: "Updated"}, body)
			_, _ = w.Write([]byte(`{"number":7,"title":"Updated","labels":[{"id":1}]}`))
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
	item, err := service.Edit(context.Background(), 7, IssueEdit{Labels: &ids, Title: &title})
	require.NoError(t, err)
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/issues/7", "PUT /api/v1/repos/alice/project/issues/7/labels"}, calls)
	require.Equal(t, "Updated", item.Title)
	require.Empty(t, item.Labels, "the labels are those of the replacement")
	calls = nil
	fail = true
	_, err = service.Edit(context.Background(), 7, IssueEdit{Labels: &ids, Title: &title})
	require.Error(t, err)
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/issues/7"}, calls, "labels are not changed when the edit fails")
}

func TestRefusedLabelChangeIsNamedAndKeepsTheOtherFields(t *testing.T) {
	var calls []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "PUT /api/v1/repos/alice/project/issues/7/labels", "PUT /api/v1/repos/alice/project/issues/8/labels":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"write permission is required"}`))
		case "PATCH /api/v1/repos/alice/project/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Updated"}`))
		case "GET /api/v1/repos/alice/project/issues/8":
			_, _ = w.Write([]byte(`{"number":8,"pull_request":{}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	service := Issues{Client: client, Repository: "alice/project"}
	ids, title := []int64{3}, "Updated"
	item, err := service.Edit(context.Background(), 7, IssueEdit{Labels: &ids, Title: &title})
	require.EqualError(t, err, "the other changes were sent, but the labels were left unchanged: Gitea refuses label changes from people who cannot write to the issues of alice/project")
	require.True(t, IsForbidden(err))
	require.Equal(t, "Updated", item.Title, "the edited issue is returned")
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/issues/7", "PUT /api/v1/repos/alice/project/issues/7/labels"}, calls)
	_, err = service.Edit(context.Background(), 8, IssueEdit{Labels: &ids})
	require.EqualError(t, err, "Gitea refuses label changes from people who cannot write to the pull requests of alice/project; nothing was changed")
}

func TestPullEditSendsThePullFieldsBeforeTheLabels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"refused", http.StatusForbidden, "the base branch was saved, but the labels were left unchanged: Gitea refuses label changes from people who cannot write to the pull requests of alice/project"},
		{"failed", http.StatusInternalServerError, "the base branch was saved, but changing the labels failed: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			pull := `{"number":7,"base":{"ref":"main"}}`
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/repos/alice/project/pulls/7":
					_, _ = w.Write([]byte(pull))
				case "PUT /api/v1/repos/alice/project/issues/7/labels":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"message":"write permission is required"}`))
				case "PATCH /api/v1/repos/alice/project/pulls/7":
					body, _ := io.ReadAll(r.Body)
					assert.Contains(t, string(body), `"base":"stable"`)
					pull = `{"number":7,"base":{"ref":"stable"}}`
					_, _ = w.Write([]byte(pull))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusTeapot)
				}
			}))
			ids, base := []int64{3}, "stable"
			item, err := (Pulls{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Labels: &ids, Base: &base})
			require.ErrorContains(t, err, tc.want)
			require.True(t, IsForbidden(err) == (tc.status == http.StatusForbidden))
			require.Equal(t, "stable", item.Base, "the edited pull request is returned")
			require.Equal(t, []string{
				"GET /api/v1/repos/alice/project/pulls/7", "PATCH /api/v1/repos/alice/project/pulls/7",
				"PUT /api/v1/repos/alice/project/issues/7/labels", "GET /api/v1/repos/alice/project/pulls/7",
			}, calls)
		})
	}
}

func TestPullEditSendsTheLabelsWhenThePullFieldsFail(t *testing.T) {
	for _, tc := range []struct {
		name        string
		labelStatus int
		want        string
	}{
		{"labels taken", http.StatusOK, `the title, the milestone and the labels were saved, but changing the base branch failed: branch "typo" not found in alice/project`},
		{
			"labels refused", http.StatusForbidden,
			`the title and the milestone were saved, but changing the base branch failed: branch "typo" not found in alice/project; ` +
				"the labels were left unchanged: Gitea refuses label changes from people who cannot write to the pull requests of alice/project",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			pull := `{"number":7,"title":"Old"}`
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.Method + " " + r.URL.Path {
				case "PATCH /api/v1/repos/alice/project/issues/7":
					pull = `{"number":7,"title":"Fix","milestone":{"id":4}}`
					_, _ = w.Write([]byte(pull))
				case "PATCH /api/v1/repos/alice/project/pulls/7":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"new base 'typo' not exist"}`))
				case "PUT /api/v1/repos/alice/project/issues/7/labels":
					w.WriteHeader(tc.labelStatus)
					if tc.labelStatus == http.StatusForbidden {
						_, _ = w.Write([]byte(`{"message":"write permission is required"}`))
						return
					}
					_, _ = w.Write([]byte(`[{"id":3,"name":"bug"}]`))
				case "GET /api/v1/repos/alice/project/pulls/7":
					_, _ = w.Write([]byte(pull))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusTeapot)
				}
			}))
			ids, title, base, milestone := []int64{3}, "Fix", "typo", int64(4)
			item, err := (Pulls{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Labels: &ids, Title: &title, Base: &base, Milestone: &milestone})
			require.EqualError(t, err, tc.want)
			_, ok := errors.AsType[*PartialEditError](err)
			require.True(t, ok, "the error names what was saved")
			require.EqualValues(t, 7, item.Number, "the pull request as saved goes with the error")
			require.Equal(t, []string{
				"GET /api/v1/repos/alice/project/pulls/7", "PATCH /api/v1/repos/alice/project/issues/7",
				"PATCH /api/v1/repos/alice/project/pulls/7", "PUT /api/v1/repos/alice/project/issues/7/labels",
				"GET /api/v1/repos/alice/project/pulls/7",
			}, calls)
		})
	}
}

func TestPullEditDoesNotChangeTheLabelsWhenNothingElseWasSaved(t *testing.T) {
	var calls []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			// Read back, the pull request holds none of the changes.
			_, _ = w.Write([]byte(`{"number":7,"base":{"ref":"main"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"branch does not exist"}`))
	}))
	ids, base := []int64{3}, "typo"
	item, err := (Pulls{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{Labels: &ids, Base: &base})
	require.Error(t, err)
	require.NotErrorAs(t, err, new(*PartialEditError))
	require.Zero(t, item.Number)
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/pulls/7", "PATCH /api/v1/repos/alice/project/pulls/7", "GET /api/v1/repos/alice/project/pulls/7"}, calls)
}

func TestPullEditWordsARefusedLabelChangeAfterReviewRequests(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Fix"}`))
		case "GET /api/v1/repos/alice/project/collaborators/carol":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/users/carol":
			_, _ = w.Write([]byte(`{"login":"carol"}`))
		case "GET /api/v1/settings/api":
			_, _ = w.Write([]byte(`{}`))
		case "GET /api/v1/repos/alice/project/pulls/7/reviews":
			writeFirstPage(w, r, `[]`)
		case "POST /api/v1/repos/alice/project/pulls/7/requested_reviewers":
			_, _ = w.Write([]byte(`[]`))
		case "PUT /api/v1/repos/alice/project/issues/7/labels":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"write permission is required"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	ids := []int64{3}
	item, err := (Pulls{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, PullEdit{
		Labels: &ids, ReviewerChanges: &ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol"}}},
	})
	require.EqualError(t, err, "the review requests were saved, but the labels were left unchanged: Gitea refuses label changes from people who cannot write to the pull requests of alice/project")
	require.True(t, IsForbidden(err))
	require.Zero(t, item.Number, "the pull request is not read back after an edit of the labels alone")
}

func TestPullEditMergesChangesWithCurrentState(t *testing.T) {
	var calls []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/pulls/7":
			// An edit of several fields reads the pull request first, and the changes merge with it.
			_, _ = w.Write([]byte(`{"number":7,"labels":[{"id":1},{"id":2}],"assignees":[{"login":"alice"},{"login":"bob"}]}`))
		case "PUT /api/v1/repos/alice/project/issues/7/labels":
			assert.JSONEq(t, `{"labels":[1,3]}`, string(body))
			_, _ = w.Write([]byte(`[]`))
		case "PATCH /api/v1/repos/alice/project/issues/7":
			assertIssueEdit(t, gitea.EditIssueOption{Assignees: []string{"alice", "carol"}}, body)
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
		"GET /api/v1/repos/alice/project/pulls/7", "PATCH /api/v1/repos/alice/project/issues/7",
		"PUT /api/v1/repos/alice/project/issues/7/labels", "GET /api/v1/repos/alice/project/pulls/7",
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
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	_, err := (Issues{Client: client, Repository: "alice/project"}).Edit(context.Background(), 7, IssueEdit{Changes: &MetadataChanges{AddLabels: []int64{3}}})
	require.NoError(t, err)
}
