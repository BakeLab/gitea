// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

func TestChecksUseLatestContextAcrossPagesAndReturnFailureWithCleanJSON(t *testing.T) {
	state := "success"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		if r.URL.Path == "/api/v1/repos/owner/repo/pulls/7" {
			_, _ = w.Write([]byte(`{"head":{"sha":"abc123"}}`))
			return
		}
		require.Equal(t, "/api/v1/repos/owner/repo/statuses/abc123", r.URL.Path)
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"context":"build","status":"failure"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"context":"build","status":"` + state + `","creator":{"login":"excluded"}}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	args := []string{"pr", "checks", "7", "-R", "owner/repo", "--json=id,name,state"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `[{"id":2,"name":"build","state":"success"}]`, out.String())
	out.Reset()
	state = "pending"
	err := Execute(context.Background(), deps, args)
	require.Equal(t, clierr.Pending, clierr.ExitCode(err))
	require.True(t, clierr.IsQuiet(err))
	require.JSONEq(t, `[{"id":2,"name":"build","state":"pending"}]`, out.String())
	out.Reset()
	state = "skipped"
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Empty(t, diagnostics.String())
}

func TestChecksWatchWaitsForRegistrationAndEmitsOneJSONResult(t *testing.T) {
	polls := 0
	keepPending := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/owner/repo/pulls/7" {
			polls++
			_, _ = w.Write([]byte(`{"head":{"sha":"abc123"}}`))
			return
		}
		if r.URL.Query().Get("page") != "1" || polls == 1 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		state := "pending"
		if polls >= 3 && !keepPending {
			state = "success"
		}
		_, _ = w.Write([]byte(`[{"id":2,"context":"build","status":"` + state + `"}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checks", "7", "-R", "owner/repo", "--watch", "--interval=1ms", "--timeout=1s", "--json=name,state"}))
	require.Equal(t, 4, polls)
	require.JSONEq(t, `[{"name":"build","state":"success"}]`, out.String())
	require.Contains(t, diagnostics.String(), "1 pending")
	keepPending = true
	out.Reset()
	diagnostics.Reset()
	err := Execute(context.Background(), deps, []string{"pr", "checks", "7", "-R", "owner/repo", "--watch", "--interval=1ms", "--timeout=20ms", "--json"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Empty(t, out.String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Execute(ctx, deps, []string{"pr", "checks", "7", "-R", "owner/repo", "--watch"})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
}

func TestChecksSummaryAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name, statuses, want string
		code                 int
	}{
		{
			name:     "pending",
			statuses: `[{"id":1,"context":"ci / test (pull_request)","status":"pending","description":"Waiting to run","target_url":"/alice/x/actions/runs/18/jobs/18"}]`,
			want: "Some checks are still pending\n0 failing, 0 successful, and 1 pending check\n\n" +
				"   NAME                      STATUS   DESCRIPTION\n" +
				"\x1b]8;;SERVER/alice/x/actions/runs/18/jobs/18\x1b\\*\x1b]8;;\x1b\\  ci / test (pull_request)  pending  Waiting to run\n",
			code: clierr.Pending,
		},
		{
			name:     "failing",
			statuses: `[{"id":1,"context":"lint","status":"failure"},{"id":2,"context":"test","status":"success"}]`,
			want:     "Some checks were not successful\n1 failing, 1 successful, and 0 pending checks\n\n   NAME  STATUS   DESCRIPTION\nX  lint  failure\n✓  test  success\n",
			code:     clierr.Failure,
		},
		{name: "none", statuses: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := checksServer(t, tc.statuses)
			deps, out, diagnostics := serverDeps(t, server.URL)
			plainTTY(t, &deps, server.URL)
			err := Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x"})
			require.Equal(t, tc.code, clierr.ExitCode(err))
			if tc.code != 0 {
				require.True(t, clierr.IsQuiet(err), "the summary already reports the outcome")
			}
			require.Equal(t, strings.ReplaceAll(tc.want, "SERVER", server.URL), out.String())
			if tc.want == "" {
				require.Equal(t, "no checks reported for pull request alice/x#8\n", diagnostics.String())
			}
		})
	}
}

func TestChecksShowLinksOnlyWithoutHyperlinks(t *testing.T) {
	server := checksServer(t, `[{"id":1,"context":"ci / test (pull_request)","status":"pending","target_url":"/alice/x/actions/runs/18/jobs/18"}]`)
	deps, out, _ := serverDeps(t, server.URL)
	plainTTY(t, &deps, server.URL)
	_ = Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x"})
	_, table, _ := strings.Cut(out.String(), "\n\n")
	require.Equal(t, "   NAME                      STATUS   DESCRIPTION\n"+
		"\x1b]8;;"+server.URL+"/alice/x/actions/runs/18/jobs/18\x1b\\*\x1b]8;;\x1b\\  ci / test (pull_request)  pending\n", table)
	// Without hyperlinks the URL is the only way to the check.
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1", "TERM": "dumb"})
	out.Reset()
	_ = Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x"})
	require.Contains(t, out.String(), "LINK\n*  ci / test (pull_request)  pending               "+server.URL+"/alice/x/actions/runs/18/jobs/18\n")
}

// checksServer serves pull request alice/x#8 whose head commit has statuses.
func checksServer(t *testing.T, statuses string) *httptest.Server {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/alice/x/pulls/8":
			_, _ = w.Write([]byte(`{"number":8,"head":{"sha":"abc"}}`))
		case r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(statuses))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestChecksWatchTimeoutCountsPendingChecks(t *testing.T) {
	t.Run("live view leaves the last summary on the normal screen", func(t *testing.T) {
		server := checksServer(t, `[{"id":1,"context":"a","status":"pending"}]`)
		deps, out, _ := serverDeps(t, server.URL)
		deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": "xterm"})
		deps.Streams.OutTTY = true
		err := Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x", "--watch", "--interval=1ms", "--timeout=30ms"})
		require.EqualError(t, err, "timed out after 30ms waiting for 1 pending check")
		_, final, ok := strings.Cut(out.String(), "\x1b[?1049l")
		require.True(t, ok)
		require.Contains(t, final, "Some checks are still pending")
	})

	server := checksServer(t, `[{"id":1,"context":"a","status":"pending"},{"id":2,"context":"b","status":"pending"}]`)
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x", "--watch", "--interval=1ms", "--timeout=30ms"})
	require.EqualError(t, err, "timed out after 30ms waiting for 2 pending checks")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestChecksWatchRedrawsOnlyOnATerminalThatInterpretsEscapes(t *testing.T) {
	server := checksServer(t, `[{"id":1,"context":"build","status":"success"}]`)
	for _, tc := range []struct {
		name, term  string
		plain, live bool
	}{
		{name: "terminal", term: "xterm", live: true},
		{name: "dumb terminal", term: "dumb"},
		{name: "console without escape sequences", term: "xterm", plain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, out, diagnostics := serverDeps(t, server.URL)
			deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": tc.term})
			deps.Streams.OutTTY, deps.Streams.OutPlain = true, tc.plain
			require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checks", "8", "-R", "alice/x", "--watch", "--interval=1ms"}))
			require.Equal(t, tc.live, strings.Contains(out.String(), "Refreshing every"))
			require.Equal(t, !tc.live, strings.Contains(diagnostics.String(), "checks: 0 failing, 1 successful, and 0 pending check\n"))
			final := out.String()
			if tc.live {
				require.True(t, strings.HasPrefix(final, "\x1b[?1049h\x1b[H\x1b[J"), "the live view redraws on the alternate screen")
				_, final, _ = strings.Cut(final, "\x1b[?1049l")
			} else {
				require.NotContains(t, final, "\x1b[")
			}
			require.Equal(t, 1, strings.Count(final, "All checks were successful"), "the final table stays on the normal screen")
			require.Contains(t, final, "build")
		})
	}
}

func TestChecksWatchSummaryBypassesThePager(t *testing.T) {
	server := checksServer(t, `[{"id":1,"context":"build","status":"success"}]`)
	run := func(t *testing.T, term string, args ...string) (*recordingPager, string) {
		deps, out, _ := serverDeps(t, server.URL)
		pager := &recordingPager{}
		deps.Pager = pager
		deps.Getenv = serverEnv(server.URL, map[string]string{"TERM": term, "PAGER": "fake-pager"})
		deps.Streams.OutTTY = true
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "checks", "8", "-R", "alice/x"}, args...)))
		return pager, out.String()
	}
	for _, term := range []string{"xterm", "dumb"} {
		pager, out := run(t, term, "--watch", "--interval=1ms")
		require.Zero(t, pager.calls, "the pager would hold gt pr checks --watch && gt pr merge back (%s)", term)
		require.Contains(t, out, "All checks were successful")
	}
	// Without --watch, the table still goes through the pager.
	pager, _ := run(t, "xterm")
	require.Equal(t, 1, pager.calls)
}
