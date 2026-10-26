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

func TestCreatesAndListingsNameWhatIsMissingOrTaken(t *testing.T) {
	answers := map[string]struct {
		status  int
		message string
	}{
		"POST /api/v1/orgs/nobody/teams":                      {404, "not found"},
		"POST /api/v1/orgs/acme/teams":                        {422, "team already exists [org_id: 151, name: devs]"},
		"POST /api/v1/admin/users":                            {409, "user already exists [name: bob]"},
		"GET /api/v1/orgs/nobody/labels":                      {404, "not found"},
		"POST /api/v1/repos/nobody/none/forks":                {404, "not found"},
		"GET /api/v1/repos/nobody/none":                       {404, "not found"},
		"POST /api/v1/repos/alice/x/forks":                    {404, "not found"},
		"GET /api/v1/repos/alice/x":                           {200, ""},
		"POST /api/v1/repos/alice/x/branches":                 {409, "The branch already exists."},
		"GET /api/v1/repos/alice/missing/labels":              {404, "not found"},
		"POST /api/v1/repos/alice/missing/branch_protections": {404, "not found"},
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := answers[r.Method+" "+r.URL.Path]
		require.True(t, ok, "unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(answer.status)
		_, _ = w.Write([]byte(`{"message":"` + answer.message + `"}`))
	}))
	ctx := context.Background()
	name, permission, email := "devs", "read", "bob@example.test"
	_, err := (Teams{Client: client}).Create(ctx, "nobody", TeamOptions{Name: &name, Permission: &permission})
	require.EqualError(t, err, `organization "nobody" not found`)
	_, err = (Teams{Client: client}).Create(ctx, "acme", TeamOptions{Name: &name, Permission: &permission})
	require.EqualError(t, err, `team "devs" already exists in acme`)
	_, err = (AdminUsers{Client: client}).Create(ctx, "bob", AdminUserOptions{Email: &email})
	require.EqualError(t, err, `user "bob" already exists`)
	_, err = (Labels{Client: client, Organization: "nobody"}).List(ctx, 30, 1)
	require.EqualError(t, err, `organization "nobody" not found`)
	_, err = (Labels{Client: client, Repository: "alice/missing"}).List(ctx, 30, 1)
	require.EqualError(t, err, `repository alice/missing not found`)
	_, _, err = (Repositories{Client: client}).Fork(ctx, "nobody/none", ForkOptions{})
	require.EqualError(t, err, `repository nobody/none not found`)
	_, _, err = (Repositories{Client: client}).Fork(ctx, "nobody/none", ForkOptions{Organization: "nobody"})
	require.EqualError(t, err, `repository nobody/none not found`)
	_, _, err = (Repositories{Client: client}).Fork(ctx, "alice/x", ForkOptions{Organization: "nobody"})
	require.EqualError(t, err, `organization "nobody" not found`)
	_, err = (Branches{Client: client}).Protect(ctx, "alice/missing", Protection{Rule: "release/*"})
	require.EqualError(t, err, `repository alice/missing not found`)
	for _, source := range []string{"", "v1.0"} {
		_, err = (Branches{Client: client}).Create(ctx, "alice/x", "main", source)
		require.EqualError(t, err, `branch "main" already exists in alice/x`)
	}
}

func TestCreatesNameAMissingRepository(t *testing.T) {
	var writes []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/x/issues/9/times":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 1, index: 9]"}`))
		case "GET /api/v1/repos/alice/x":
			_, _ = w.Write([]byte(`{"full_name":"alice/x"}`))
		case "GET /api/v1/repos/alice/x/actions/variables/V":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"variable not found"}`))
		case "POST /api/v1/repos/alice/x/actions/variables/V":
			w.WriteHeader(http.StatusCreated)
		default:
			// Gitea answers a missing repository or organization with a bare 404.
			if r.Method != http.MethodGet {
				writes = append(writes, r.Method+" "+r.URL.Path)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	ctx := context.Background()
	_, err := (DeployKeys{Client: client, Repository: "alice/typo"}).Create(ctx, "ci", "ssh-ed25519 AAAA", true)
	require.EqualError(t, err, "repository alice/typo not found")
	for _, source := range []string{"", "v1.0"} {
		_, err = (Branches{Client: client}).Create(ctx, "alice/typo", "feature", source)
		require.EqualError(t, err, "repository alice/typo not found")
	}
	_, err = (Times{Client: client, Repository: "alice/typo"}).Create(ctx, 1, 60)
	require.EqualError(t, err, "repository alice/typo not found")
	_, err = (Times{Client: client, Repository: "alice/x"}).Create(ctx, 9, 60)
	require.EqualError(t, err, "issue #9 not found in alice/x")
	for _, scope := range []ActionScope{{Repository: "alice/typo"}, {Organization: "nobody"}} {
		want := "repository alice/typo not found"
		if scope.Organization != "" {
			want = "organization nobody not found"
		}
		_, err = (ActionSecrets{Client: client, Scope: scope}).Set(ctx, "S", "x", "")
		require.EqualError(t, err, want)
		_, err = (ActionVariables{Client: client, Scope: scope}).Set(ctx, "V", "x", nil)
		require.EqualError(t, err, want)
	}
	require.NotContains(t, writes, "POST /api/v1/repos/alice/typo/actions/variables/V", "a missing repository is no missing variable")
	_, err = (ActionVariables{Client: client, Scope: ActionScope{Repository: "alice/x"}}).Set(ctx, "V", "x", nil)
	require.NoError(t, err)
}

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
		{"wiki page exists by its file name", "wiki page already exists [title: dir%2Fx.md]", "wiki page", `"dir/x"`, 400, `wiki page "dir/x" already exists in alice/x`},
		{"unnamed wiki page exists", "wiki page already exists [title: Home.md]", "wiki page", "", 400, `wiki page "Home" already exists in alice/x`},
		{"archived label is no repository", "label is archived", "issue", "#1", 422, "Gitea API returned HTTP 422 for POST /api/v1/repos/alice/x/endpoint: label is archived"},
		{"run completed", "run is already completed", "run", "35", 409, "run 35 is already completed"},
		{"run not done", "this workflow run is not done", "run", "37", 400, "run 37 has not finished"},
		{"workflow disabled", "workflow is disabled", "workflow", "nightly.yaml", 403, "workflow nightly.yaml is disabled"},
		{"key in use", "Key content has been used as non-deploy key", "SSH key", "", 422, "this SSH key is already registered"},
		{"unknown assignee", "Assignee does not exist: [name: user does not exist [uid: 0, name: nobody123]]", "issue", "", 422, `user "nobody123" not found`},
		{"unknown account name", "user redirect does not exist [name: nosuch]", "", "", 404, `user "nosuch" not found`},
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

func TestClassifyNamesAMissingBranchNotThePullRequest(t *testing.T) {
	for _, message := range []string{`ref "develp" does not exist`, `new base 'develp' not exist`} {
		err := Classify(&transport.APIError{Status: http.StatusNotFound, Message: message}, "pull request", "#3", "alice/x")
		require.EqualError(t, err, `branch "develp" not found in alice/x`)
	}
}
