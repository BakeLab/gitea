// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestClassifyWordsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, message, kind, key string
		status                   int
		want                     string
	}{
		{"missing issue", "not found", "issue", "#999", 404, "issue #999 not found in alice/x"},
		{"archived 423", "repo is archived", "issue", "", 423, "alice/x is archived (read-only)"},
		{"archived 500", "<Repository 5:alice/x> is archived", "release", "", 500, "alice/x is archived (read-only)"},
		{"pull request exists", "pull request already exists for these targets [id: 16, issue_id: 1, head_repo_id: 44, base_repo_id: 44, head_branch: feat, base_branch: main]", "pull request", "", 409, "pull request for feat into main already exists in alice/x"},
		{"no changes", "Invalid PullRequest: There are no changes between the head and the base", "pull request", "", 422, "no commits between the base and head branches"},
		{"merged", "cannot change state of this pull request, it was already merged", "pull request", "#3", 412, "pull request #3 is already merged"},
		{"draft merge", "Work in progress PRs cannot be merged", "pull request", "#5", 405, "pull request #5 is a draft"},
		{"own approval", "approve your own pull is not allowed", "pull request", "#1", 422, "you can't approve or request changes on your own pull request"},
		{"own rejection", "reject your own pull is not allowed", "pull request", "#1", 422, "you can't approve or request changes on your own pull request"},
		{"branch exists", "The branch already exists.", "branch", "", 409, "branch already exists in alice/x"},
		{"release exists", "Release is has no Tag", "release", "", 409, "release already exists in alice/x"},
		{"wiki page exists", "wiki page already exists [title: Home.md]", "wiki page", `"Home"`, 400, `wiki page "Home" already exists in alice/x`},
		{"archived label is no repository", "label is archived", "issue", "#1", 422, "Gitea API returned HTTP 422 for POST /api/v1/repos/alice/x/endpoint: label is archived"},
		{"run completed", "run is already completed", "run", "35", 409, "run 35 is already completed"},
		{"run not done", "this workflow run is not done", "run", "37", 400, "run 37 has not finished"},
		{"workflow disabled", "workflow is disabled", "workflow", "nightly.yaml", 403, "workflow nightly.yaml is disabled"},
		{"key in use", "Key content has been used as non-deploy key", "SSH key", "", 422, "this SSH key is already registered"},
		{"unknown assignee", "Assignee does not exist: [name: user does not exist [uid: 0, name: nobody123]]", "issue", "", 422, `user "nobody123" not found`},
		{"server error", "boom", "issue", "#1", 500, "Gitea API returned HTTP 500 for POST /api/v1/repos/alice/x/endpoint: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := &transport.APIError{Status: tc.status, Method: "POST", Endpoint: "/api/v1/repos/alice/x/endpoint", Message: tc.message}
			err := Classify(apiErr, tc.kind, tc.key, "alice/x")
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, apiErr, "the API error stays reachable for exit codes and --debug")
		})
	}
}

func TestClassifyUsesTheCallersNameForTheResource(t *testing.T) {
	apiErr := &transport.APIError{Status: http.StatusNotFound, Method: "GET", Endpoint: "/api/v1/repos/alice/x/releases/5", Message: "not found"}
	err := Classify(apiErr, "release", "v1.0", "alice/x")
	var notFound *NotFoundError
	require.ErrorAs(t, err, &notFound)
	require.EqualError(t, err, "release v1.0 not found in alice/x")
	conflict := Classify(&transport.APIError{Status: http.StatusConflict, Message: "Release is has no Tag"}, "release", "v1.0", "alice/x")
	require.EqualError(t, conflict, "release v1.0 already exists in alice/x")
	require.Equal(t, errors.New("plain"), Classify(errors.New("plain"), "release", "v1.0", "alice/x"))
	require.ErrorIs(t, Classify(context.Canceled, "release", "v1.0", "alice/x"), context.Canceled)
}

func TestConflictHintsFollowTheReason(t *testing.T) {
	err := &ConflictError{Reason: ReasonArchived, Scope: "alice/old", Hint: "unarchive it with: gt repo edit alice/old --archived=false"}
	require.EqualError(t, err, "alice/old is archived (read-only); unarchive it with: gt repo edit alice/old --archived=false")
}
