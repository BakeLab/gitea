// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gitea.dev/cli/internal/clierr"

	"github.com/stretchr/testify/require"
)

func TestTimeListShowsRepositoryAndDurations(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/user/times", r.URL.Path)
		_, _ = w.Write([]byte(`[
			{"id":1,"time":5100,"created":"2026-10-01T10:00:00Z","user_name":"alice","issue":{"number":3,"repository":{"full_name":"alice/project"}}},
			{"id":2,"time":900,"created":"2026-10-02T10:00:00Z","user_name":"alice","issue":{"number":3,"repository":{"full_name":"bob/tools"}}}
		]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--total"}))
	require.Equal(t, "1\talice/project#3\talice\t1h25m0s\t2026-10-01T10:00:00Z\n"+
		"2\tbob/tools#3\talice\t15m0s\t2026-10-02T10:00:00Z\n"+
		"Total: 1h40m0s\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--json=repository,issue_number"}))
	require.JSONEq(t, `[{"repository":"alice/project","issue_number":3},{"repository":"bob/tools","issue_number":3}]`, out.String())
}

func TestTimeCreateReadsDurationsAndWholeSecondTimes(t *testing.T) {
	var seconds []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST /api/v1/repos/alice/project/issues/1/times", r.Method+" "+r.URL.Path)
		seconds = append(seconds, payloadField(t, r, "time"))
		_, _ = w.Write([]byte(`{"id":5,"time":5400,"created":"2026-10-11T18:57:49.588823137Z","user_name":"alice"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, duration := range []string{"1h30m", "1h 30m", " 90m "} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"time", "create", "1", duration, "-R", "alice/project", "--json=created_at"}))
		require.JSONEq(t, `{"created_at":"2026-10-11T18:57:49Z"}`, out.String())
	}
	require.Equal(t, []string{"5400", "5400", "5400"}, seconds)
	for duration, want := range map[string]string{
		"2d":   `invalid duration "2d": use h, m and s units such as 1h25m`,
		"0m":   `invalid duration "0m": time spent must be positive`,
		"1.5s": `invalid duration "1.5s": use whole seconds`,
	} {
		err := Execute(context.Background(), deps, []string{"time", "create", "1", duration, "-R", "alice/project"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), duration)
		require.ErrorContains(t, err, want)
	}
	require.Len(t, seconds, 3)
}

func TestTimeTablesAndDoneLines(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/times", "GET /api/v1/repos/alice/project/issues/1/times":
			writeFirstPage(w, r, `[{"id":8,"time":5400,"created":"2026-10-11T18:00:00Z","user_name":"alice","issue":{"number":1,"title":"Login fails","repository":{"full_name":"alice/project"}}},`+
				`{"id":9,"time":900,"created":"2026-10-11T19:00:00Z","user_name":"bob","issue":{"number":2,"title":"Docs"}}]`)
		case "POST /api/v1/repos/alice/project/issues/1/times":
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":10,"time":5100,"user_name":"alice","issue":{"number":1,"title":"Login fails"}}`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return testNow }
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	deps.Streams.OutTTY = true
	run := func(args ...string) error {
		out.Reset()
		diagnostics.Reset()
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
	}
	require.NoError(t, run("time", "list"))
	require.Equal(t, "Showing 2 of 2 time records in alice/project\n\n"+
		"ID  ISSUE           USER   SPENT   WHEN\n"+
		"8   #1 Login fails  alice  1h 30m  about 2 hours ago\n"+
		"9   #2 Docs         bob    15m     about 1 hour ago\n"+
		"Total: 1h 45m\n", out.String())
	require.NoError(t, run("time", "add", "#1", "1h 25m"))
	require.Equal(t, "✓ Logged 1h 25m on alice/project#1 (Login fails)\n", diagnostics.String())
	require.NoError(t, run("time", "delete", "1", "8", "--yes"))
	require.Equal(t, "✓ Deleted 1h 30m logged by alice on alice/project#1\n", diagnostics.String())
	require.NoError(t, run("time", "reset", "1", "--yes"))
	require.Equal(t, "✓ Deleted your time logged on alice/project#1\n", diagnostics.String())
	require.ErrorContains(t, run("time", "delete", "1", "77", "--yes"), "tracked time 77 not found in alice/project#1")
	require.EqualError(t, run("time", "delete", "1", "8"), "confirmation required to delete 1h 30m logged by alice on alice/project#1; rerun with --yes")
	require.Equal(t, []string{
		"POST /api/v1/repos/alice/project/issues/1/times",
		"DELETE /api/v1/repos/alice/project/issues/1/times/8",
		"DELETE /api/v1/repos/alice/project/issues/1/times",
	}, changed)
	for seconds, want := range map[int64]string{0: "0s", 45: "45s", 90: "1m 30s", 900: "15m", 3600: "1h", 5400: "1h 30m", 5410: "1h 30m", -90: "-1m 30s"} {
		require.Equal(t, want, spent(seconds))
	}
}

func TestTimeNamesWhatIsMissing(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/9/times", "DELETE /api/v1/repos/alice/project/issues/9/times":
			_, _ = w.Write([]byte(`{"message":"issue does not exist [id: 0, repo_id: 1, index: 9]"}`))
		case "DELETE /api/v1/repos/alice/project/issues/1/times":
			_, _ = w.Write([]byte(`{"message":"tracked_time does not exist"}`))
		case "GET /api/v1/repos/alice/gone/times", "GET /api/v1/repos/alice/gone":
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	run := func(args ...string) error {
		return Execute(context.Background(), deps, append(args, "--no-input", "--yes"))
	}
	require.EqualError(t, run("time", "list", "--issue", "9", "-R", "alice/project"), "issue #9 not found in alice/project")
	require.EqualError(t, run("time", "delete", "9", "8", "-R", "alice/project"), "issue #9 not found in alice/project")
	require.EqualError(t, run("time", "reset", "9", "-R", "alice/project"), "issue #9 not found in alice/project")
	require.EqualError(t, run("time", "list", "-R", "alice/gone"), "repository alice/gone not found")
	require.NoError(t, run("time", "reset", "1", "-R", "alice/project"))
	require.Equal(t, "! You have no time logged on alice/project#1\n", diagnostics.String())
}

func TestTimeListTotalPipedCoversEmptyAndPagedListings(t *testing.T) {
	empty := true
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if empty {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.Header().Set("X-Total-Count", "2")
		_, _ = w.Write([]byte(`[{"id":1,"time":60,"created":"2026-10-01T10:00:00Z","user_name":"alice","issue":{"number":3,"repository":{"full_name":"alice/project"}}}]`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--total"}))
	require.Equal(t, "Total: 0s\n", out.String())
	out.Reset()
	empty = false
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--total", "--limit", "1"}))
	require.Equal(t, "1\talice/project#3\talice\t1m0s\t2026-10-01T10:00:00Z\nTotal: 1m0s\n", out.String())
	require.Equal(t, "more results available; use --page 2\n", diagnostics.String())
}

func TestCommentListHintsAtTheNextClientSidePage(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"body":"a","user":{"login":"alice"}},{"id":2,"body":"b","user":{"login":"alice"}}]`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "list", "3", "-R", "alice/project", "--limit", "1"}))
	require.Equal(t, "more results available; use --page 2\n", diagnostics.String())
}

func TestPipedTimeKeepsExactSeconds(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"time":3723,"created":"2026-10-01T10:00:00Z","user_name":"alice","issue":{"number":3,"repository":{"full_name":"alice/project"}}}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--total"}))
	require.Equal(t, "1\talice/project#3\talice\t1h2m3s\t2026-10-01T10:00:00Z\nTotal: 1h2m3s\n", out.String())
	require.Equal(t, "1h 2m", spent(3723))
}
