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

func TestClassifyRequestNamesTheResource(t *testing.T) {
	for _, tc := range []struct {
		name, method, endpoint, message string
		status                          int
		want                            string
	}{
		{"issue", "GET", "/api/v1/repos/alice/project/issues/999", "not found", 404, "issue #999 not found in alice/project"},
		{"issue comments", "GET", "/api/v1/repos/alice/project/issues/9/comments", "not found", 404, "issue #9 not found in alice/project"},
		{"comment", "DELETE", "/api/v1/repos/alice/project/issues/comments/81", "comment does not exist [id: 81, issue_id: 0]", 404, "comment 81 not found in alice/project"},
		{"pull request", "GET", "/api/v1/repos/alice/project/pulls/7/reviews", "not found", 404, "pull request #7 not found in alice/project"},
		{"release tag", "GET", "/api/v1/repos/alice/project/releases/tags/v9", "not found", 404, "release v9 not found in alice/project"},
		{"branch with slashes", "GET", "/api/v1/repos/alice/project/branches/docs/guide", "not found", 404, "branch docs/guide not found in alice/project"},
		{"milestone title", "GET", "/api/v1/repos/alice/project/milestones/v2.0", "not found", 404, `milestone "v2.0" not found in alice/project`},
		{"milestone id", "GET", "/api/v1/repos/alice/project/milestones/3", "not found", 404, "milestone 3 not found in alice/project"},
		{"wiki page", "GET", "/api/v1/repos/alice/project/wiki/page/Nope", "not found", 404, `wiki page "Nope" not found in alice/project`},
		{"run", "GET", "/api/v1/repos/alice/project/actions/runs/9999/jobs", "run with id 9999: resource does not exist", 404, "run 9999 not found in alice/project"},
		{"secret", "DELETE", "/api/v1/repos/alice/project/actions/secrets/NOPE", "secret was not found [name: ]", 404, "secret NOPE not found in alice/project"},
		{"repository", "GET", "/api/v1/repos/nobody/none", "not found", 404, "repository nobody/none not found"},
		{"user", "GET", "/api/v1/users/nobody/repos", "user redirect does not exist [name: nobody]", 404, `user "nobody" not found`},
		{"organization", "GET", "/api/v1/orgs/nope-org", "not found", 404, `organization "nope-org" not found`},
		{"org member", "DELETE", "/api/v1/orgs/acme/members/carol", "not found", 404, `member "carol" not found in acme`},
		{"team", "GET", "/api/v1/teams/99", "not found", 404, "team 99 not found"},
		{"ssh key", "DELETE", "/api/v1/user/keys/5", "not found", 404, "SSH key 5 not found"},
		{"archived 423", "POST", "/api/v1/repos/alice/old-stuff/issues", "repo is archived", 423, "alice/old-stuff is archived (read-only)"},
		{"archived 500", "POST", "/api/v1/repos/alice/old-stuff/releases", "<Repository 5:alice/old-stuff> is archived", 500, "alice/old-stuff is archived (read-only)"},
		{"archived repository edit", "PATCH", "/api/v1/repos/alice/old-stuff", "repo is archived", 423, "alice/old-stuff is archived (read-only)"},
		{"pull request exists", "POST", "/api/v1/repos/alice/x/pulls", "pull request already exists for these targets [id: 16, issue_id: 1, head_repo_id: 44, base_repo_id: 44, head_branch: feat, base_branch: main]", 409, "pull request for feat into main already exists in alice/x"},
		{"no changes", "POST", "/api/v1/repos/alice/x/pulls", "Invalid PullRequest: There are no changes between the head and the base", 422, "no commits between the base and head branches"},
		{"merged", "PATCH", "/api/v1/repos/alice/x/pulls/3", "cannot change state of this pull request, it was already merged", 412, "pull request #3 is already merged"},
		{"merge merged", "POST", "/api/v1/repos/alice/x/pulls/3/merge", "The PR is already merged", 405, "pull request #3 is already merged"},
		{"draft merge", "POST", "/api/v1/repos/alice/x/pulls/5/merge", "Work in progress PRs cannot be merged", 405, "pull request #5 is a draft"},
		{"own approval", "POST", "/api/v1/repos/alice/x/pulls/1/reviews", "approve your own pull is not allowed", 422, "you can't approve or request changes on your own pull request"},
		{"own rejection", "POST", "/api/v1/repos/alice/x/pulls/1/reviews", "reject your own pull is not allowed", 422, "you can't approve or request changes on your own pull request"},
		{"branch exists", "POST", "/api/v1/repos/alice/x/branches", "The branch already exists.", 409, "branch already exists in alice/x"},
		{"release exists", "POST", "/api/v1/repos/alice/x/releases", "Release is has no Tag", 409, "release already exists in alice/x"},
		{"wiki page exists", "POST", "/api/v1/repos/alice/x/wiki/new", "wiki page already exists [title: Home.md]", 400, `wiki page "Home" already exists in alice/x`},
		{"repository exists", "POST", "/api/v1/user/repos", "The repository with the same name already exists.", 409, "repository already exists"},
		{"org repository exists", "POST", "/api/v1/orgs/acme/repos", "The repository with the same name already exists.", 409, "repository already exists"},
		{"run completed", "POST", "/api/v1/repos/alice/x/actions/runs/35/cancel", "run is already completed", 409, "run 35 is already completed"},
		{"workflow disabled", "POST", "/api/v1/repos/alice/x/actions/workflows/nightly.yaml/dispatches", "workflow is disabled", 403, "workflow nightly.yaml is disabled"},
		{"key in use", "POST", "/api/v1/user/keys", "Key content has been used as non-deploy key", 422, "this SSH key is already registered"},
		{"unknown assignee", "POST", "/api/v1/repos/alice/x/issues", "Assignee does not exist: [name: user does not exist [uid: 0, name: nobody123]]", 422, `user "nobody123" not found`},
		{"unknown poster filter", "GET", "/api/v1/repos/alice/x/pulls", "user does not exist [uid: 0, name: nobody]", 400, `user "nobody" not found`},
		{"unrecognized endpoint", "GET", "/api/v1/repos/alice/x/issues", "not found", 404, "not found (HTTP 404 GET /api/v1/repos/alice/x/issues)"},
		{"server error", "GET", "/api/v1/repos/alice/x/issues/1", "boom", 500, "Gitea API returned HTTP 500 for GET /api/v1/repos/alice/x/issues/1: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := &transport.APIError{Status: tc.status, Method: tc.method, Endpoint: tc.endpoint, Message: tc.message}
			err := ClassifyRequest(apiErr)
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
