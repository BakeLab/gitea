// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

const dependencyPullJSON = `{"number":7,"title":"Fix","state":"open","mergeable":true,"head":{"ref":"fix","label":"fix","sha":"abc1234","repo":{"full_name":"alice/project"}},"base":{"ref":"main","repo":{"full_name":"alice/project"}}}`

// dependencyServer serves issue 12 and pull request 7 of alice/project, which both take dependency
// changes, and records the requests it gets.
type dependencyServer struct {
	mu       sync.Mutex
	requests []string
}

func (s *dependencyServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		path := r.URL.Path[len("/api/v1/repos/alice/project"):]
		switch r.Method + " " + path {
		case "POST /issues/12/dependencies", "DELETE /issues/12/dependencies", "POST /issues/12/blocks", "DELETE /issues/12/blocks",
			"POST /issues/7/dependencies", "DELETE /issues/7/dependencies", "POST /issues/7/blocks", "DELETE /issues/7/blocks":
			var meta map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&meta))
			s.requests = append(s.requests, fmt.Sprintf("%s %s %v/%v#%v", r.Method, path, meta["owner"], meta["repo"], meta["index"]))
			_, _ = w.Write([]byte(`{"number":1}`))
		case "GET /issues/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Crash"}`))
		case "GET /issues/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Fix","pull_request":{"merged":false}}`))
		case "GET /pulls/7":
			_, _ = w.Write([]byte(dependencyPullJSON))
		case "GET /issues/7/dependencies", "GET /issues/7/blocks":
			if page := r.URL.Query().Get("page"); page != "" && page != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			s.requests = append(s.requests, "GET "+path)
			if path == "/issues/7/blocks" {
				_, _ = w.Write([]byte(`[{"number":20,"repository":{"full_name":"bob/lib"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"number":9,"repository":{"full_name":"alice/project"}}]`))
		case "GET ":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`))
		case "GET /pulls/7/reviews":
			_, _ = w.Write([]byte(`[]`))
		default:
			if r.Method != http.MethodGet {
				t.Errorf("unexpected %s %s", r.Method, path)
			}
			_, _ = w.Write([]byte(`{}`)) // what pr view reads besides the pull request
		}
	})
}

func (s *dependencyServer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := s.requests
	s.requests = nil
	return requests
}

func TestEditAddsAndRemovesDependenciesInBothDirections(t *testing.T) {
	state := &dependencyServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) (string, error) {
		deps, out, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
		return out.String(), err
	}

	_, err := run("issue", "edit", "12", "--add-blocked-by", "9", "--add-blocks", "#10,bob/lib#5", "--remove-blocks", "4")
	require.NoError(t, err)
	require.Equal(t, []string{
		"DELETE /issues/12/blocks alice/project#4",
		"POST /issues/12/dependencies alice/project#9",
		"POST /issues/12/blocks alice/project#10",
		"POST /issues/12/blocks bob/lib#5",
	}, state.take(), "the issue in the path blocks the one in the body")

	_, err = run("pr", "edit", "7", "--add-blocked-by", "9", "--remove-blocked-by", "8", "--add-blocks", "20", "--remove-blocks", "21")
	require.NoError(t, err)
	require.Equal(t, []string{
		"DELETE /issues/7/dependencies alice/project#8",
		"DELETE /issues/7/blocks alice/project#21",
		"POST /issues/7/dependencies alice/project#9",
		"POST /issues/7/blocks alice/project#20",
	}, state.take(), "a pull request has dependencies like an issue, and nothing else is sent")
}

func TestEditRefusesContradictoryDependencies(t *testing.T) {
	state := &dependencyServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "edit", "12", "--add-blocks", "alice/project#12"}, "--add-blocks alice/project#12 names the edited issue; an issue cannot block itself"},
		{[]string{"issue", "edit", "12", "--add-blocks", "9", "--remove-blocks", "9"}, "alice/project#9 is both added and removed as a blocked issue"},
		{[]string{"issue", "edit", "12", "--add-blocked-by", "9", "--remove-blocked-by", "9"}, "alice/project#9 is both added and removed as a blocker"},
		{[]string{"issue", "edit", "12", "--add-blocked-by", "9", "--add-blocks", "9"}, "alice/project#9 cannot both block and be blocked by the edited issue"},
		{[]string{"issue", "edit", "12", "--add-blocks", "lib#5"}, "--add-blocks takes issues as N, #N, OWNER/REPO#N or an issue URL"},
		{[]string{"pr", "edit", "7", "--add-blocked-by", "7"}, "names the edited issue"},
		{[]string{"pr", "edit", "7", "--add-blocks", "9", "--add-blocked-by", "9"}, "cannot both block and be blocked by the edited issue"},
	} {
		deps, _, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append(tc.args, "-R", "alice/project", "--no-input"))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v: %v", tc.args, err)
		require.ErrorContains(t, err, tc.want)
	}
	require.Empty(t, state.take(), "nothing is sent for a contradiction")

	// Changing a dependency to and from the same issue in the other direction is not a
	// contradiction: it flips it, in both directions.
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "--remove-blocked-by", "9", "--add-blocks", "9", "-R", "alice/project", "--no-input"}))
	require.Equal(t, []string{"DELETE /issues/12/dependencies alice/project#9", "POST /issues/12/blocks alice/project#9"}, state.take())
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "--remove-blocks", "9", "--add-blocked-by", "9", "-R", "alice/project", "--no-input"}))
	require.Equal(t, []string{"DELETE /issues/12/blocks alice/project#9", "POST /issues/12/dependencies alice/project#9"}, state.take())
}

func TestPullEditChecksThePullRequestBeforeItsDependencies(t *testing.T) {
	var writes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project")
		switch {
		case r.Method != http.MethodGet:
			writes = append(writes, r.Method+" "+path)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":9}`))
		case path == "/pulls/12":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case path == "/issues/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"I","state":"open"}`))
		default:
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true,"has_pull_requests":true}`))
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "edit", "12", "--add-blocks", "9", "-R", "alice/project", "--no-input"})
	require.ErrorContains(t, err, "is an issue, not a pull request")
	require.Empty(t, writes)
}

func TestOtherPullCommandsLeaveDependenciesAlone(t *testing.T) {
	state := &dependencyServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "reopen", "7", "-R", "alice/project", "--json", "--no-input"}))
	require.NotContains(t, out.String(), "blocked_by")
	require.NotContains(t, state.take(), "GET /issues/7/blocks")
}

func TestPullViewOutputsDependenciesAsJSON(t *testing.T) {
	state := &dependencyServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	run := func(args ...string) string {
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "view", "7", "-R", "alice/project", "--no-input"}, args...)))
		return out.String()
	}

	out := run("--json", "number,blocked_by,blocks")
	require.JSONEq(t, `{"number":7,"blocked_by":[{"repository":"alice/project","number":9}],"blocks":[{"repository":"bob/lib","number":20}]}`, out)
	require.ElementsMatch(t, []string{"GET /issues/7/dependencies", "GET /issues/7/blocks"}, state.take())

	out = run("--json", "number,title")
	require.JSONEq(t, `{"number":7,"title":"Fix"}`, out)
	require.Empty(t, state.take(), "the dependencies cost requests, made only when the output includes them")

	out = run("--json", "number", "--jq", ".number")
	require.Equal(t, "7\n", out)
	require.Empty(t, state.take())
}
