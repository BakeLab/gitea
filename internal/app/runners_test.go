// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

const runnerJSON = `{"id":3,"name":"builder-1","status":"online","busy":true,"disabled":false,"ephemeral":false,"labels":[{"id":1,"name":"ubuntu-latest","type":"custom"},{"id":2,"name":"docker","type":"custom"}]}`

// runnerServer answers the runner endpoints of every scope and records the requests it gets.
type runnerServer struct {
	mu       sync.Mutex
	requests []string
	bodies   []string
	disabled bool
}

func (s *runnerServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		s.requests = append(s.requests, r.Method+" "+path+"?"+r.URL.RawQuery)
		s.bodies = append(s.bodies, string(body))
		runnerBody := func() string {
			return strings.Replace(runnerJSON, `"disabled":false`, `"disabled":`+strconv.FormatBool(s.disabled), 1)
		}
		runner := runnerBody()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/actions/runners"):
			_, _ = w.Write([]byte(`{"runners":[` + runner + `],"total_count":1}`))
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/runners/registration-token"):
			_, _ = w.Write([]byte(`{"token":"reg-token-123"}`))
		case strings.Contains(path, "/actions/runners/3"):
			switch r.Method {
			case http.MethodGet:
				_, _ = w.Write([]byte(runner))
			case http.MethodPatch:
				s.disabled = strings.Contains(string(body), `"disabled":true`)
				_, _ = w.Write([]byte(runnerBody()))
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			}
		case path == "/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestRunnerCommandsUseTheSelectedScope(t *testing.T) {
	state := &runnerServer{}
	server := newAuthenticatedServer(state.handler(t))
	t.Cleanup(server.Close)
	run := func(args ...string) string {
		t.Helper()
		deps, out, _ := testDeps(t)
		deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
		require.NoError(t, Execute(context.Background(), deps, append([]string{"runner"}, args...)))
		return out.String()
	}

	for _, tc := range []struct {
		flags         []string
		base, setting string
	}{
		{nil, "/repos/alice/project/actions/runners", "/alice/project/settings"},
		{[]string{"--org", "acme"}, "/orgs/acme/actions/runners", "/org/acme/settings"},
		{[]string{"--account"}, "/user/actions/runners", "/user/settings"},
		{[]string{"--global"}, "/admin/actions/runners", "/-/admin"},
	} {
		t.Run(strings.Join(tc.flags, " "), func(t *testing.T) {
			state.requests = nil
			require.Equal(t, "3\tbuilder-1\tonline\tubuntu-latest, docker\tbusy\n", run(append([]string{"list"}, tc.flags...)...))
			require.Equal(t, "GET "+tc.base+"?limit=30&page=1", state.requests[0])
			require.Contains(t, run(append([]string{"view", "3"}, tc.flags...)...), "url:\t"+server.URL+tc.setting+"/actions/runners/3\n")
			require.Equal(t, "GET "+tc.base+"/3?", state.requests[1])
			require.Equal(t, "reg-token-123\n", run(append([]string{"token"}, tc.flags...)...))
			require.Equal(t, "POST "+tc.base+"/registration-token?", state.requests[2])
			run(append([]string{"delete", "3", "--yes"}, tc.flags...)...)
			require.Equal(t, "DELETE "+tc.base+"/3?", state.requests[len(state.requests)-1])
		})
	}

	t.Run("disable and enable", func(t *testing.T) {
		state.requests, state.bodies = nil, nil
		run("disable", "3")
		require.Equal(t, []string{"GET /repos/alice/project/actions/runners/3?", "PATCH /repos/alice/project/actions/runners/3?"}, state.requests)
		require.JSONEq(t, `{"disabled":true}`, state.bodies[1])
		state.requests = nil
		run("disable", "3") // already disabled: nothing is sent
		require.Equal(t, []string{"GET /repos/alice/project/actions/runners/3?"}, state.requests)
		run("enable", "3")
		require.JSONEq(t, `{"disabled":false}`, state.bodies[len(state.bodies)-1])
		require.JSONEq(t, `{"id":3,"name":"builder-1","status":"online","busy":true,"disabled":false,"ephemeral":false,"labels":["ubuntu-latest","docker"]}`, run("view", "3", "--json"))
	})

	t.Run("filters", func(t *testing.T) {
		state.requests = nil
		run("list", "--disabled")
		run("list", "--enabled")
		require.Equal(t, "GET /repos/alice/project/actions/runners?disabled=true&limit=30&page=1", state.requests[0])
		require.Equal(t, "GET /repos/alice/project/actions/runners?disabled=false&limit=30&page=1", state.requests[1])
	})

	t.Run("token as JSON", func(t *testing.T) {
		require.JSONEq(t, `{"token":"reg-token-123"}`, run("token", "--json"))
	})
}

func TestRunnerMissingRunnerNamesTheRunner(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	err := Execute(context.Background(), deps, []string{"runner", "view", "9"})
	require.ErrorContains(t, err, "runner 9")
	require.NotEqual(t, clierr.Usage, clierr.ExitCode(err))
}
