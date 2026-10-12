// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

// fakeGitea answers the requests in responses, keyed by "METHOD path", with a status and body,
// and every other request with Gitea's bare 404.
func fakeGitea(t *testing.T, responses map[string][2]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, ok := responses[r.Method+" "+r.URL.Path]
		if !ok {
			response = [2]string{"404", `{"message":"not found"}`}
		}
		var status int
		_, _ = fmt.Sscan(response[0], &status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response[1]))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestExplainNamesResourcesAndTellsWhyRepositoriesFail(t *testing.T) {
	server := fakeGitea(t, map[string][2]string{
		"GET /api/v1/user":                                {"200", `{"login":"alice"}`},
		"GET /api/v1/repos/alice/project":                 {"200", `{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`},
		"GET /api/v1/repos/alice/project/issues/1":        {"200", `{"number":1,"pull_request":null}`},
		"GET /api/v1/repos/bob/project":                   {"200", `{"full_name":"bob/project","has_issues":false,"has_pull_requests":true}`},
		"GET /api/v1/repos/alice/empty":                   {"200", `{"full_name":"alice/empty","empty":true,"has_issues":true,"has_pull_requests":true}`},
		"GET /api/v1/repos/alice/old":                     {"200", `{"full_name":"alice/old","archived":true,"has_issues":true}`},
		"POST /api/v1/repos/alice/old/issues":             {"423", `{"message":"repo is archived"}`},
		"POST /api/v1/repos/alice/project/pulls":          {"409", `{"message":"pull request already exists for these targets [id: 16, issue_id: 3, head_repo_id: 1, base_repo_id: 1, head_branch: feat, base_branch: main]"}`},
		"GET /api/v1/repos/alice/project/pulls/main/feat": {"200", `{"number":3,"html_url":"https://example.org/alice/project/pulls/3"}`},
		"GET /api/v1/repos/alice/project/pulls/3":         {"200", `{"number":3,"state":"open","draft":true,"head":{"sha":"abc"}}`},
		"POST /api/v1/repos/alice/project/pulls/3/merge":  {"405", `{"message":"Work in progress PRs cannot be merged"}`},
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing issue", []string{"issue", "view", "999", "-R", "alice/project"}, "issue #999 not found in alice/project"},
		{"missing repository", []string{"issue", "list", "-R", "alice/nope"}, "repository alice/nope not found on " + server.URL + " (or you don't have access)"},
		{"disabled issues", []string{"issue", "list", "-R", "bob/project"}, "issues are disabled for bob/project"},
		{"issue as pull request", []string{"pr", "view", "1", "-R", "alice/project"}, "#1 in alice/project is an issue, not a pull request; view it with: gt issue view 1"},
		{"missing pull request", []string{"pr", "view", "999", "-R", "alice/project"}, "pull request #999 not found in alice/project"},
		{"empty repository", []string{"pr", "list", "-R", "alice/empty"}, "alice/empty is empty; it has no pull requests"},
		{"archived repository", []string{"issue", "create", "-R", "alice/old", "--title", "x", "--body", "y"}, "alice/old is archived (read-only); unarchive it with: gt repo edit alice/old --archived=false"},
		{"existing pull request", []string{"pr", "create", "-R", "alice/project", "--head", "feat", "--base", "main", "--title", "x", "--body", "y"}, "pull request for feat into main already exists in alice/project; see https://example.org/alice/project/pulls/3"},
		{"context added by the command", []string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, "pull request #3 is a draft; mark it ready for review first: Gitea API returned HTTP 405 for POST /api/v1/repos/alice/project/pulls/3/merge: Work in progress PRs cannot be merged"},
		{"raw api response", []string{"api", "users/nobody"}, "not found (HTTP 404 GET /api/v1/users/nobody)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := serverDeps(t, server.URL)
			err := Execute(context.Background(), deps, append(tc.args, "--no-input"))
			require.Error(t, err)
			explained := Explain(context.Background(), deps, err)
			require.EqualError(t, explained, tc.want)
			require.Equal(t, clierr.ExitCode(err), clierr.ExitCode(explained))
		})
	}
}

func TestExplainAddsTheNextStepToRefusals(t *testing.T) {
	for _, tc := range []struct {
		method, endpoint, message string
		status                    int
		want                      string
	}{
		{"PATCH", "/api/v1/repos/alice/x/pulls/3", "cannot change state of this pull request, it was already merged", 412, "pull request #3 is already merged; its state can no longer change"},
		{"POST", "/api/v1/repos/alice/x/pulls/3/merge", "The PR is already merged", 405, "pull request #3 is already merged"},
		{"POST", "/api/v1/repos/alice/x/pulls/5/merge", "Work in progress PRs cannot be merged", 405, "pull request #5 is a draft; mark it ready with: gt pr edit 5 --ready"},
		{"POST", "/api/v1/repos/alice/x/pulls/1/reviews", "approve your own pull is not allowed", 422, "you can't approve or request changes on your own pull request; leave a comment instead"},
		{"POST", "/api/v1/repos/alice/x/pulls", "Invalid PullRequest: There are no changes between the head and the base", 422, "no commits between the base and head branches; check out your feature branch or pass --head"},
		{"POST", "/api/v1/repos/alice/x/actions/workflows/nightly.yaml/dispatches", "workflow is disabled", 403, "workflow nightly.yaml is disabled; enable it with: gt workflow enable nightly.yaml"},
		{"DELETE", "/api/v1/repos/alice/x/actions/runs/37", "this workflow run is not done", 400, "run 37 has not finished; cancel it first: gt run cancel 37"},
		{"POST", "/api/v1/repos/alice/x/wiki/new", "wiki page already exists [title: Home.md]", 400, `wiki page "Home" already exists in alice/x; edit it with: gt wiki edit "Home"`},
		{"DELETE", "/api/v1/admin/users/uxadm1", "user still has ownership of repositories [uid: 7]", 422, `user "uxadm1" still owns repositories; transfer or delete them first`},
		{"POST", "/api/v1/user/keys", "Key content has been used as non-deploy key", 422, "this SSH key is already registered"},
	} {
		apiErr := &transport.APIError{Status: tc.status, Method: tc.method, Endpoint: tc.endpoint, Message: tc.message, Instance: "http://localhost:3001"}
		require.EqualError(t, Explain(context.Background(), Dependencies{}, apiErr), tc.want, tc.endpoint)
	}
}

func TestExplainWordsCredentialAndConnectionFailures(t *testing.T) {
	rejecting := fakeGitea(t, map[string][2]string{"GET /api/v1/user": {"401", `{"message":"token is invalid"}`}})
	deps, _, _ := serverDeps(t, rejecting.URL)
	err := Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project"})
	explained := Explain(context.Background(), deps, err)
	require.EqualError(t, explained, "GITEA_TOKEN was rejected by "+rejecting.URL+"; fix or unset GITEA_TOKEN")
	require.Equal(t, clierr.Authentication, clierr.ExitCode(explained))

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	deps, _, _ = serverDeps(t, closed.URL)
	err = Execute(context.Background(), deps, []string{"issue", "list", "-R", "alice/project"})
	require.EqualError(t, Explain(context.Background(), deps, err), "cannot reach "+closed.URL+" (connection refused); check --host or your network")
}

func TestTimeoutErrorsNameTheWaitAndKeepTheExitCode(t *testing.T) {
	err := fmt.Errorf("watch: %w", context.DeadlineExceeded)
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{4 * time.Second, "timed out after 4s waiting for run 34"},
		{time.Hour, "timed out after 1h waiting for run 34"},
		{90 * time.Minute, "timed out after 1h30m waiting for run 34"},
		{2 * time.Minute, "timed out after 2m waiting for run 34"},
	} {
		timeout := timeoutError(err, tc.d, "waiting for run %d", 34)
		require.EqualError(t, timeout, tc.want)
		require.ErrorIs(t, timeout, context.DeadlineExceeded)
		require.Equal(t, clierr.ExitCode(err), clierr.ExitCode(timeout))
		require.Equal(t, timeout, Explain(context.Background(), Dependencies{}, timeout), "explained errors are kept")
	}
	other := errors.New("boom")
	require.Equal(t, other, timeoutError(other, time.Second, "waiting"))
	require.EqualError(t, Explain(context.Background(), Dependencies{}, err), "timed out before the operation finished")
}
