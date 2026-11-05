// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIssueLockPinAndDelete runs the commands against a server that serves issues 5 and 6 and the
// pull request 7 of alice/project, and records the writes it gets.
func TestIssueLockPinAndDelete(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			writes = append(writes, strings.TrimSpace(r.Method+" "+path+" "+strings.TrimSpace(string(body))))
			if path == "/issues/6/lock" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"no permission to lock this issue"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch path {
		case "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
		case "/issues/5":
			_, _ = w.Write([]byte(`{"number":5,"title":"Bug","state":"open"}`))
		case "/issues/6":
			_, _ = w.Write([]byte(`{"number":6,"title":"Other","state":"open"}`))
		case "/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Fix","state":"open","pull_request":{"merged":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist"}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		writes = nil
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append(args, "--no-input"))
		return out.String(), err
	}

	out, err := run("issue", "lock", "5", "--reason", "Resolved", "--json", "number,title")
	require.NoError(t, err)
	require.JSONEq(t, `{"number":5,"title":"Bug"}`, out)
	require.Equal(t, []string{`PUT /issues/5/lock {"lock_reason":"Resolved"}`}, writes)

	_, err = run("issue", "unlock", "5")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /issues/5/lock"}, writes)

	_, err = run("issue", "pin", "5", "7")
	require.NoError(t, err)
	require.Equal(t, []string{"POST /issues/5/pin", "POST /issues/7/pin"}, writes, "a pull request can be pinned too")

	_, err = run("issue", "unpin", "5")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /issues/5/pin"}, writes)

	t.Run("a failure does not stop the others", func(t *testing.T) {
		_, err := run("issue", "lock", "6", "5")
		require.ErrorContains(t, err, "issue 6")
		require.ErrorContains(t, err, "no permission to lock this issue")
		require.Equal(t, []string{"PUT /issues/6/lock {\"lock_reason\":\"\"}", "PUT /issues/5/lock {\"lock_reason\":\"\"}"}, writes)
	})

	t.Run("delete", func(t *testing.T) {
		_, err := run("issue", "delete", "5", "6", "--yes")
		require.NoError(t, err)
		require.Equal(t, []string{"DELETE /issues/5", "DELETE /issues/6"}, writes)

		_, err = run("issue", "delete", "5", "7", "--yes")
		require.ErrorContains(t, err, "is a pull request; gt does not delete pull requests")
		require.Empty(t, writes, "a pull request stops the batch before any issue is deleted")

		_, err = run("issue", "delete", "404", "--yes")
		require.ErrorContains(t, err, "issue 404")
		require.Empty(t, writes)

		_, err = run("issue", "delete", "5")
		require.Error(t, err, "without --yes and a terminal there is nobody to ask")
		require.Empty(t, writes)
	})
}

func TestIssueActionsReportWhatTheyDidNotDo(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		if r.Method != http.MethodGet {
			writes = append(writes, r.Method+" "+path)
			if path == "/issues/6" || path == "/issues/6/pin" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"it failed"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch path {
		case "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
		case "/issues/8":
			_, _ = w.Write([]byte(`{"number":8,"title":"Locked","state":"open","is_locked":true}`))
		default:
			_, _ = w.Write([]byte(`{"number":` + path[strings.LastIndex(path, "/")+1:] + `,"title":"Open","state":"open"}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		writes = nil
		deps, _, diagnostics := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append(args, "--no-input"))
		return diagnostics.String(), err
	}

	stderr, err := run("issue", "lock", "8", "--reason", "Spam")
	require.NoError(t, err)
	require.Contains(t, stderr, "Issue alice/project#8 (Locked) is already locked")
	require.Empty(t, writes)

	_, err = run("issue", "pin", "6")
	require.ErrorContains(t, err, "it may be pinned already, or too many are")

	stderr, err = run("issue", "delete", "5", "6", "--yes")
	require.ErrorContains(t, err, "issue 6")
	require.Contains(t, stderr, "Deleted issue alice/project#5", "a script learns what is gone")
}

func TestIssueDeleteAsksOnceForTheWholeBatch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/repos/alice/project") {
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
			return
		}
		n := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		_, _ = w.Write([]byte(`{"number":` + n + `,"title":"T` + n + `","state":"open"}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "delete", "5", "6"}))
	require.Equal(t, []string{"Delete 2 issues of alice/project (#5, #6)?"}, prompt.confirmations)

	prompt = &scriptedPrompter{accept: true}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "delete", "5"}))
	require.Equal(t, []string{"Delete issue alice/project#5 (T5)?"}, prompt.confirmations)
}
