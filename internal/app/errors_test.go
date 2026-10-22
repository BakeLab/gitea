// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
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

func TestExplainKeepsClassifiedErrorsAndAddsHints(t *testing.T) {
	server := fakeGitea(t, map[string][2]string{
		"GET /api/v1/user":                                {"200", `{"login":"alice"}`},
		"GET /api/v1/repos/alice/project":                 {"200", `{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`},
		"GET /api/v1/repos/alice/old":                     {"200", `{"full_name":"alice/old","archived":true,"has_issues":true}`},
		"POST /api/v1/repos/alice/old/issues":             {"423", `{"message":"repo is archived"}`},
		"POST /api/v1/repos/alice/project/pulls":          {"409", `{"message":"pull request already exists for these targets [id: 16, issue_id: 3, head_repo_id: 1, base_repo_id: 1, head_branch: feat, base_branch: main]"}`},
		"GET /api/v1/repos/alice/project/pulls/main/feat": {"200", `{"number":2,"html_url":"https://gitea.test/alice/project/pulls/2"}`},
		"GET /api/v1/repos/alice/project/pulls/3":         {"200", `{"number":3,"state":"open","mergeable":true,"head":{"sha":"abc"}}`},
		"POST /api/v1/repos/alice/project/pulls/3/merge":  {"405", `{"message":"Does not have enough approvals"}`},
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing issue", []string{"issue", "view", "999", "-R", "alice/project"}, "issue #999 not found in alice/project"},
		{"missing pull request", []string{"pr", "view", "999", "-R", "alice/project"}, "pull request #999 not found in alice/project"},
		{"archived repository", []string{"issue", "create", "-R", "alice/old", "--title", "x", "--body", "y"}, "alice/old is archived (read-only); unarchive it with: gt repo unarchive alice/old"},
		{"existing pull request", []string{"pr", "create", "-R", "alice/project", "--head", "feat", "--base", "main", "--title", "x", "--body", "y"}, "pull request for feat into main already exists in alice/project; see https://gitea.test/alice/project/pulls/2"},
		{"context added by the command", []string{"pr", "merge", "3", "-R", "alice/project", "--yes"}, "pull request #3 cannot be merged: Gitea API returned HTTP 405 for POST /api/v1/repos/alice/project/pulls/3/merge: Does not have enough approvals"},
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
		method, kind, key, message string
		status                     int
		want                       string
	}{
		{"PATCH", "pull request", "#3", "cannot change state of this pull request, it was already merged", 412, "pull request #3 is already merged; its state can no longer change"},
		{"POST", "pull request", "#3", "The PR is already merged", 405, "pull request #3 is already merged"},
		{"POST", "pull request", "#5", "Work in progress PRs cannot be merged", 405, "pull request #5 is a draft; mark it ready with: gt pr ready 5"},
		{"POST", "pull request", "#1", "approve your own pull is not allowed", 422, "you can't approve or request changes on your own pull request; leave a comment instead"},
		{"POST", "pull request", "", "Invalid PullRequest: There are no changes between the head and the base", 422, "no commits between the base and head branches; check out your feature branch or pass --head"},
		{"POST", "workflow", "nightly.yaml", "workflow is disabled", 403, "workflow nightly.yaml is disabled; enable it with: gt workflow enable nightly.yaml"},
		{"DELETE", "run", "37", "this workflow run is not done", 400, "run 37 has not finished; cancel it first: gt run cancel 37"},
		{"POST", "issue", "", "repo is archived", 423, "alice/x is archived (read-only); unarchive it with: gt repo unarchive alice/x"},
		{"DELETE", "user", `"uxadm1"`, "user still has ownership of repositories [uid: 7]", 422, `user "uxadm1" still owns repositories; transfer them with gt repo transfer or delete them first`},
		{"POST", "SSH key", "", "Key content has been used as non-deploy key", 422, "this SSH key is already registered"},
	} {
		apiErr := &transport.APIError{Status: tc.status, Method: tc.method, Endpoint: "/api/v1/repos/alice/x/endpoint", Message: tc.message, Instance: "http://localhost:3001"}
		require.EqualError(t, Explain(context.Background(), Dependencies{}, services.Classify(apiErr, tc.kind, tc.key, "alice/x")), tc.want, tc.message)
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

	// An unreachable proxy is named instead of the instance behind it.
	proxy, err := url.Parse(closed.URL)
	require.NoError(t, err)
	_, err = (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}).Get("http://example.test/api/v1/user")
	err = transport.RequestError(context.Background(), "API request failed", err)
	require.EqualError(t, Explain(context.Background(), deps, err), "cannot reach the proxy "+proxy.Host+" (connection refused); check HTTPS_PROXY and HTTP_PROXY")
}

func TestExplainSuggestsTheLoginThatGrantsAMissingScope(t *testing.T) {
	message := "token does not have at least one of required scope(s), required=[write:user], token scope=read:user"
	server := fakeGitea(t, map[string][2]string{"GET /api/v1/user/repos": {"403", `{"message":"` + message + `"}`}})
	deps, _, _ := testDeps(t)
	configureAccounts(t, deps, server.URL, "alice", "alice", "bob")
	explained := func() string {
		err := Execute(context.Background(), deps, []string{"repo", "list"})
		return Explain(context.Background(), deps, err).Error()
	}
	token := "create a token with that scope and log in with it: gt auth login --with-token"
	oauth := "gt auth login --host " + server.URL + " --oauth --scopes write:user"
	require.Equal(t, message+"; "+token, explained())
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: map[string]settings.Account{
		"alice": {AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreFile},
		"bob":   {AuthMethod: settings.AuthToken, CredentialStore: settings.StoreFile},
	}})
	require.Equal(t, message+"; "+token+", or for an OAuth account: "+oauth, explained())
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: map[string]settings.Account{"alice": {AuthMethod: settings.AuthOAuth, CredentialStore: settings.StoreFile}}})
	require.Equal(t, message+"; log in again with that scope: "+oauth, explained())
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
	// A request that ran out of http_timeout is not the end of --timeout.
	stalled := &transport.RequestFailure{Action: "reading the API response failed", Err: fmt.Errorf("no data received within 2s: %w", transport.ErrTimeout)}
	require.Equal(t, error(stalled), timeoutError(stalled, time.Hour, "waiting for run %d", 34))
	require.Equal(t, clierr.ExitCode(err), clierr.ExitCode(stalled))
	require.EqualError(t, Explain(context.Background(), Dependencies{}, stalled), "the instance did not respond within http_timeout; raise it with: gt config set http_timeout DURATION")
	require.EqualError(t, Explain(context.Background(), Dependencies{}, err), "timed out before the operation finished")
}

func TestHTTPTimeoutNamesTheServerAndTheSetting(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)
	deps, _, _ := serverDeps(t, server.URL)
	deps.Client = transport.New
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_HTTP_TIMEOUT": "50ms"})
	err := Explain(context.Background(), deps, Execute(context.Background(), deps, []string{"api", "version"}))
	require.EqualError(t, err, server.URL+" did not respond within http_timeout (50ms); raise GITEA_CLI_HTTP_TIMEOUT")
	require.ErrorIs(t, err, context.DeadlineExceeded, "it exits like a timeout")

	deps.Getenv = serverEnv(server.URL, nil)
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "http_timeout", "50ms"}))
	err = Explain(context.Background(), deps, Execute(context.Background(), deps, []string{"api", "version"}))
	require.EqualError(t, err, server.URL+" did not respond within http_timeout (50ms); raise it with: gt config set http_timeout DURATION")
}

func TestRejectedLoginTokenIsNotBlamedOnTheEnvironment(t *testing.T) {
	rejecting := fakeGitea(t, map[string][2]string{"GET /api/v1/user": {"401", `{"message":"token is invalid"}`}})
	deps, _, _ := testDeps(t)
	deps.Getenv = envFunc(map[string]string{"GITEA_TOKEN": "valid-environment-token"})
	deps.Streams.In = strings.NewReader("mistyped-token\n")
	err := Execute(context.Background(), deps, []string{"auth", "login", "--host", rejecting.URL, "--with-token", "--credential-store", "file", "--no-input"})
	explained := Explain(context.Background(), deps, err)
	require.EqualError(t, explained, "the token was rejected by "+rejecting.URL+"; check it or create a new one under Settings > Applications")
	require.Equal(t, clierr.Authentication, clierr.ExitCode(explained))
}
