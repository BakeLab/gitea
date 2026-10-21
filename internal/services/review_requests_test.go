// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewRequestNamesValidatedBeforeNetwork(t *testing.T) {
	for _, changes := range []ReviewRequestChanges{
		{},
		{Add: ReviewRequestInput{Users: []string{""}}},
		{Add: ReviewRequestInput{Teams: []string{" team"}}},
		{Add: ReviewRequestInput{Users: []string{"Alice", "alice"}}},
		{Add: ReviewRequestInput{Users: []string{"Alice"}}, Remove: ReviewRequestInput{Users: []string{"alice"}}},
		{Add: ReviewRequestInput{Teams: []string{"Team"}}, Remove: ReviewRequestInput{Teams: []string{"team"}}},
	} {
		require.Error(t, changes.Validate())
	}
	require.NoError(t, (ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"alice@example.test"}, Teams: []string{"alice"}}}).Validate())
}

func TestReviewRequestPartialFailureStopsFurtherEdits(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/users/") {
			_, _ = w.Write([]byte(`{"login":"` + strings.TrimPrefix(r.URL.Path, "/api/v1/users/") + `"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/settings/api":
			_, _ = w.Write([]byte(`{}`))
			return
		case "/api/v1/repos/owner/repo/pulls/1/reviews":
			writeFirstPage(w, r, `[{"id":1,"state":"REQUEST_REVIEW","user":{"login":"alice"}}]`)
			return
		}
		mutations++
		require.Equal(t, "/api/v1/repos/owner/repo/pulls/1/requested_reviewers", r.URL.Path)
		var input ReviewRequestInput
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&input))
		if mutations == 1 {
			require.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, ReviewRequestInput{Users: []string{"alice"}}, input)
			w.WriteHeader(http.StatusNoContent)
		} else {
			require.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, ReviewRequestInput{Users: []string{"bob"}}, input)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	title := "New title"
	changes := ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"alice"}}, Add: ReviewRequestInput{Users: []string{"bob"}}}
	_, err := (Pulls{Client: client, Repository: "owner/repo"}).Edit(context.Background(), 1, PullEdit{Title: &title, ReviewerChanges: &changes})
	require.ErrorContains(t, err, "reviewer removals succeeded")
	require.Equal(t, 2, mutations)
}

func TestReviewRequestNamesWhatIsMissingWhenNothingChanged(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		repoExists    bool
		changes       ReviewRequestChanges
		want          string
	}{
		{
			"missing pull request", "pull request does not exist [id: 0, issue_id: 0, head_repo_id: 0, base_repo_id: 3, head_branch: , base_branch: ]", true,
			ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"bob"}}},
			"pull request #9 not found in owner/repo",
		},
		{
			"missing repository", "not found", false,
			ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"bob"}}},
			"repository owner/repo not found",
		},
		{
			"unknown email reviewer", "user doesn't exist: carol@example.test", true,
			ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol@example.test"}}},
			`user "carol@example.test" not found`,
		},
		{
			"unknown team", "team doesn't exist: devs", true,
			ReviewRequestChanges{Add: ReviewRequestInput{Teams: []string{"devs"}}},
			`team "devs" not found in owner`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/users/bob":
					_, _ = w.Write([]byte(`{"login":"bob"}`))
				case r.URL.Path == "/api/v1/repos/owner/repo" && tc.repoExists:
					_, _ = w.Write([]byte(`{"full_name":"owner/repo"}`))
				case r.URL.Path == "/api/v1/repos/owner/repo":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
				case r.URL.Path == "/api/v1/repos/owner/repo/pulls/9/requested_reviewers":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"` + tc.message + `"}`))
				default:
					// The team check cannot see the organization and leaves the name to Gitea.
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			_, err := (Reviews{Client: client, Repository: "owner/repo"}).ChangeRequests(context.Background(), 9, tc.changes)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestReviewerTeamCheckSkippedWhenTeamListingIsForbidden(t *testing.T) {
	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/owner":
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v1/orgs/owner/teams":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Must be an organization member"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			posted = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"number":1}`))
		}
	}))
	defer server.Close()
	changes := ReviewRequestChanges{Add: ReviewRequestInput{Teams: []string{"devs"}}}
	_, err := (Reviews{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}).ChangeRequests(context.Background(), 1, changes)
	require.NoError(t, err)
	require.True(t, posted, "the server decides whether the team exists")
}

func TestUnknownReviewerTeamRejectsBeforeUserRemoval(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/orgs/owner":
			_, _ = w.Write([]byte(`{"id":1}`))
		case "/api/v1/orgs/owner/teams":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	changes := ReviewRequestChanges{Remove: ReviewRequestInput{Users: []string{"alice"}}, Add: ReviewRequestInput{Teams: []string{"missing"}}}
	_, err := (Reviews{Client: client, Repository: "owner/repo"}).ChangeRequests(context.Background(), 1, changes)
	require.ErrorContains(t, err, `team "missing" is unavailable`)
}

func TestReviewerTeamCheckSkippedForAHiddenPrivateOrganization(t *testing.T) {
	var posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/owner" || r.URL.Path == "/api/v1/users/owner":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"GetUserByName"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			posted = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"number":1}`))
		}
	}))
	defer server.Close()
	changes := ReviewRequestChanges{Add: ReviewRequestInput{Teams: []string{"devs"}}}
	_, err := (Reviews{Client: transport.New(server.URL, "", false), Repository: "owner/repo"}).ChangeRequests(context.Background(), 1, changes)
	require.NoError(t, err)
	require.True(t, posted, "a non-member cannot see the organization, so the server decides")
}

func TestReviewRequestLeavesEmailReviewersToTheServer(t *testing.T) {
	var posted ReviewRequestInput
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls/1/requested_reviewers":
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&posted))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/owner/repo/pulls/1":
			_, _ = w.Write([]byte(`{"number":1}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	changes := ReviewRequestChanges{Add: ReviewRequestInput{Users: []string{"carol@example.test"}}}
	_, err := (Reviews{Client: client, Repository: "owner/repo"}).ChangeRequests(context.Background(), 1, changes)
	require.NoError(t, err)
	require.Equal(t, []string{"carol@example.test"}, posted.Users)
}
