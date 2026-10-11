// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/tea/internal/clierr"
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
	require.ErrorContains(t, err, "not all succeeded")
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
