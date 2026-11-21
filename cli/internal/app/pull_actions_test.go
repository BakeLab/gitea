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

const pullActionJSON = `{"number":7,"title":"Fix","state":"open","mergeable":true,"head":{"ref":"fix","label":"fix","sha":"abc1234","repo":{"full_name":"alice/project"}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`

// TestPullLockPinAndDelete runs the commands against a server that serves the pull request 7 and the
// issue 5 of alice/project, and records the writes it gets.
func TestPullLockPinAndDelete(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		if r.Method != http.MethodGet {
			body, _ := io.ReadAll(r.Body)
			writes = append(writes, strings.TrimSpace(r.Method+" "+path+" "+strings.TrimSpace(string(body))))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch path {
		case "":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`))
		case "/pulls/7":
			_, _ = w.Write([]byte(pullActionJSON))
		case "/issues/5":
			_, _ = w.Write([]byte(`{"number":5,"title":"Bug","state":"open"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) (string, error) {
		writes = nil
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		err := Execute(context.Background(), deps, append([]string{"pr"}, append(args, "--no-input")...))
		return out.String(), err
	}

	out, err := run("lock", "7", "--reason", "Resolved", "--json", "number,title")
	require.NoError(t, err)
	require.JSONEq(t, `{"number":7,"title":"Fix"}`, out)
	require.Equal(t, []string{`PUT /issues/7/lock {"lock_reason":"Resolved"}`}, writes, "Gitea serves pull request locks through the issue endpoint")

	_, err = run("unlock", "7")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /issues/7/lock"}, writes)

	_, err = run("pin", "7")
	require.NoError(t, err)
	require.Equal(t, []string{"POST /issues/7/pin"}, writes)

	_, err = run("unpin", "7")
	require.NoError(t, err)
	require.Equal(t, []string{"DELETE /issues/7/pin"}, writes)

	t.Run("an issue number is refused by every command", func(t *testing.T) {
		for _, verb := range []string{"lock", "unlock", "pin", "unpin", "delete"} {
			_, err := run(verb, "5", "--yes")
			require.ErrorContains(t, err, "is an issue, not a pull request", verb)
			require.Empty(t, writes, verb)
		}
	})

	t.Run("delete", func(t *testing.T) {
		_, err := run("delete", "7", "--yes")
		require.NoError(t, err)
		require.Equal(t, []string{"DELETE /issues/7"}, writes)

		_, err = run("delete", "7")
		require.Error(t, err, "without --yes and a terminal there is nobody to ask")
		require.Empty(t, writes)

		_, err = run("delete", "404", "--yes")
		require.ErrorContains(t, err, "404")
		require.Empty(t, writes)

		_, err = run("delete", "7", "--yes", "--json")
		require.ErrorContains(t, err, "pull request deletion does not return a resource")
	})
}

func TestPullDeleteNeedsANumberForAClosedPullRequest(t *testing.T) {
	var writes []string
	closed := strings.Replace(pullActionJSON, `"state":"open"`, `"state":"closed","merged":true`, 1)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		switch {
		case r.Method != http.MethodGet:
			writes = append(writes, r.Method+" "+path)
			w.WriteHeader(http.StatusNoContent)
		case path == "/pulls" && r.URL.Query().Get("state") != "open":
			_, _ = w.Write([]byte("[" + closed + "]"))
		case path == "/pulls":
			_, _ = w.Write([]byte("[]"))
		case path == "/pulls/7":
			_, _ = w.Write([]byte(closed))
		default:
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
		}
	}))
	defer server.Close()
	run := func(args ...string) error {
		deps, _, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		return Execute(context.Background(), deps, append(args, "--yes", "--no-input"))
	}

	require.Error(t, run("pr", "delete", "fix"))
	require.Empty(t, writes, "a reused branch name must not delete an old pull request")

	require.NoError(t, run("pr", "delete", "7"))
	require.Equal(t, []string{"DELETE /issues/7"}, writes)
}

func TestPullLockReportsOneLockedAlready(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/pulls/7") {
			_, _ = w.Write([]byte(strings.Replace(pullActionJSON, `"state":"open"`, `"state":"open","is_locked":true`, 1)))
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
	}))
	defer server.Close()
	deps, _, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "lock", "7", "--reason", "Spam", "--no-input"}))
	require.Contains(t, diagnostics.String(), "is already locked")
}

func TestPullDeleteAsksFirst(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pulls/7") {
			_, _ = w.Write([]byte(pullActionJSON))
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project","has_pull_requests":true}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "delete", "7"}))
	require.Equal(t, []string{"Delete pull request alice/project#7 (Fix)?"}, prompt.confirmations)
}
