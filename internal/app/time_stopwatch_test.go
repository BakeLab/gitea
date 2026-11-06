// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// stopwatchWatches are the stopwatches of alice that stopwatchServer can have running, one at a time
// as in Gitea.
var stopwatchWatches = map[string]string{
	"alice/project#12": `{"seconds":3725,"duration":"1h2m5s","issue_index":12,"issue_title":"Crash","repo_owner_name":"alice","repo_name":"project","created":"2026-10-11T10:00:00Z"}`,
	"bob/lib#3":        `{"seconds":30,"issue_index":3,"issue_title":"Other","repo_owner_name":"bob","repo_name":"lib","created":"2026-10-11T11:00:00Z"}`,
}

// stopwatchServer serves the stopwatch of alice, which runs on the issue named by running if any,
// and the stopwatch requests of issue 12 of alice/project, formerly alice/old, and records what it is
// sent.
type stopwatchServer struct {
	mu          sync.Mutex
	requests    []string
	running     string
	listRefused bool
}

func (s *stopwatchServer) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := s.requests
	s.requests = nil
	return requests
}

func (s *stopwatchServer) set(running string, listRefused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running, s.listRefused = running, listRefused
}

func (s *stopwatchServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		s.mu.Lock()
		defer s.mu.Unlock()
		const stopwatch = "/repos/alice/project/issues/12/stopwatch/"
		onThisIssue := strings.HasSuffix(s.running, "#12")
		switch {
		case r.Method == http.MethodGet && path == "/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","has_issues":true}`))
		case r.Method == http.MethodGet && path == "/repos/alice/project/issues/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Crash","state":"open"}`))
		case r.Method == http.MethodGet && path == "/user/stopwatches":
			switch page := r.URL.Query().Get("page"); {
			case s.listRefused:
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"token does not have at least one of required scope(s), required=[read:user]"}`))
			case page != "" && page != "1" || s.running == "":
				_, _ = w.Write([]byte(`[]`))
			default:
				_, _ = w.Write([]byte(`[` + stopwatchWatches[s.running] + `]`))
			}
		case strings.HasPrefix(path, "/repos/alice/project/issues/404/stopwatch/"):
			s.requests = append(s.requests, r.Method+" "+strings.TrimPrefix(path, "/repos/alice/project/issues/404/stopwatch/"))
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 1, index: 404]"}`))
		case strings.HasPrefix(path, "/repos/alice/gone"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"The target couldn't be found."}`))
		case strings.HasPrefix(path, "/repos/alice/old/"):
			http.Redirect(w, r, strings.Replace(r.URL.Path, "/alice/old/", "/alice/project/", 1), http.StatusMovedPermanently)
		case path == stopwatch+"start", path == stopwatch+"stop", path == stopwatch+"delete":
			s.requests = append(s.requests, r.Method+" "+strings.TrimPrefix(path, stopwatch))
			switch strings.TrimPrefix(path, stopwatch) {
			case "start":
				if onThisIssue {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"message":"cannot start a stopwatch again if it already exists"}`))
					return
				}
				s.running = "alice/project#12"
				w.WriteHeader(http.StatusCreated)
			case "stop":
				if !onThisIssue {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"message":"cannot stop a non-existent stopwatch"}`))
					return
				}
				s.running = ""
				w.WriteHeader(http.StatusCreated)
			default:
				if !onThisIssue {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"message":"cannot cancel a non-existent stopwatch"}`))
					return
				}
				s.running = ""
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestTimeStopwatch(t *testing.T) {
	state := &stopwatchServer{}
	server := newAuthenticatedServer(state.handler(t))
	defer server.Close()
	runIn := func(repo string, args ...string) (string, string, error) {
		deps, out, diagnostics := serverDeps(t, server.URL)
		deps.Streams.ErrTTY = true
		deps.Streams.OutTTY = args[0] != "status"
		extra := []string{"-R", repo, "--no-input"}
		if args[0] == "status" {
			extra = []string{"--no-input"}
		}
		err := Execute(context.Background(), deps, append([]string{"time"}, append(args, extra...)...))
		return out.String(), diagnostics.String(), err
	}
	run := func(args ...string) (string, string, error) { return runIn("alice/project", args...) }

	_, _, err := run("stop", "12")
	require.ErrorContains(t, err, "no stopwatch is running on alice/project#12; start one with: gt time start 12")
	require.Equal(t, []string{"POST stop"}, state.take(), "Gitea decides whether one runs")
	_, _, err = run("cancel", "12", "--yes")
	require.EqualError(t, err, "no stopwatch is running on alice/project#12")
	require.Equal(t, []string{"DELETE delete"}, state.take())

	for _, args := range [][]string{{"stop"}, {"cancel", "--yes"}} {
		_, _, err = run(append(args, "404")...)
		require.ErrorContains(t, err, "issue #404 not found in alice/project", "%v", args)
		state.take()
		_, _, err = runIn("alice/gone", append(args, "12")...)
		require.ErrorContains(t, err, "repository alice/gone not found", "%v", args)
		_, _, err = runIn("alice/old", append(args, "12")...)
		require.ErrorContains(t, err, "repository moved to alice/project", "%v", args)
		require.Empty(t, state.take(), "Gitea's 301 is not followed for a change")
	}

	_, progress, err := run("start", "12")
	require.NoError(t, err)
	require.Contains(t, progress, "Started a stopwatch on alice/project#12")
	require.NotContains(t, progress, "Stopped")
	require.Equal(t, []string{"POST start"}, state.take())
	_, _, err = run("start", "12")
	require.EqualError(t, err, "a stopwatch is already running on alice/project#12", "Gitea's 409, in words")
	state.take()

	out, _, err := run("status", "--json", "repository,issue_number,seconds,started_at")
	require.NoError(t, err)
	require.JSONEq(t, `[{"repository":"alice/project","issue_number":12,"seconds":3725,"started_at":"2026-10-11T10:00:00Z"}]`, out)
	out, _, err = run("status")
	require.NoError(t, err)
	require.Contains(t, out, "alice/project#12 Crash\t1h2m5s\t")

	_, progress, err = run("stop", "12")
	require.NoError(t, err)
	require.Contains(t, progress, "Stopped the stopwatch on alice/project#12 (Crash) after 1h 2m and logged the time")
	require.Equal(t, []string{"POST stop"}, state.take())

	state.set("bob/lib#3", false)
	out, _, err = run("status")
	require.NoError(t, err)
	require.Contains(t, out, "bob/lib#3 Other\t30s\t", "in any repository, not only the selected one")
	_, progress, err = run("start", "12")
	require.NoError(t, err)
	require.Contains(t, progress, "Stopped the stopwatch on bob/lib#3 (Other) after 30s and logged its time")
	require.Contains(t, progress, "Started a stopwatch on alice/project#12")
	state.take()

	state.set("bob/lib#3", true)
	_, progress, err = run("start", "12")
	require.NoError(t, err, "a listing that fails does not prevent the start")
	require.Contains(t, progress, "Started a stopwatch on alice/project#12")
	require.Equal(t, []string{"POST start"}, state.take())

	// Without the listing it still stops, without its details.
	state.set("alice/project#12", true)
	_, progress, err = run("stop", "12")
	require.NoError(t, err)
	require.Contains(t, progress, "Stopped the stopwatch on alice/project#12 and logged the time")
	require.Equal(t, []string{"POST stop"}, state.take())

	state.set("alice/project#12", true)
	_, _, err = run("cancel", "12")
	require.Error(t, err, "cancel still asks")
	require.Empty(t, state.take())
	_, progress, err = run("cancel", "12", "--yes")
	require.NoError(t, err)
	require.Contains(t, progress, "Deleted the stopwatch on alice/project#12\n")
	require.Equal(t, []string{"DELETE delete"}, state.take())

	state.set("alice/project#12", false)
	require.Error(t, func() error { _, _, err := run("cancel", "12"); return err }(), "without --yes and a terminal there is nobody to ask")
	require.Empty(t, state.take())
	_, progress, err = run("cancel", "12", "--yes")
	require.NoError(t, err)
	require.Contains(t, progress, "Deleted the stopwatch on alice/project#12 (Crash), which has run for 1h 2m")
	require.Equal(t, []string{"DELETE delete"}, state.take())
	_, _, err = run("status")
	require.NoError(t, err)
}
