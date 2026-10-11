// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLabelCreateAndEditExclusiveArchived(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		switch calls {
		case 1:
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/labels", r.URL.Path)
			require.Equal(t, "scoped", payload["name"])
			require.Equal(t, "00aa00", payload["color"])
			require.Equal(t, true, payload["exclusive"])
			_, _ = w.Write([]byte(`{"id":3,"name":"scoped","color":"00aa00","description":"","exclusive":true,"is_archived":false}`))
		case 2:
			require.Equal(t, http.MethodPatch, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/labels/3", r.URL.Path)
			require.Equal(t, true, payload["is_archived"])
			require.Equal(t, false, payload["exclusive"])
			_, _ = w.Write([]byte(`{"id":3,"name":"scoped","color":"00aa00","description":"","exclusive":false,"is_archived":true}`))
		default:
			t.Fatalf("unexpected call %d %s %s", calls, r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	fields := "--json=id,name,color,exclusive,is_archived"
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "create", "-R", "owner/repo", "--name", "scoped", "--color", "00aa00", "--exclusive", fields, "--no-input"}))
	require.JSONEq(t, `{"id":3,"name":"scoped","color":"00aa00","exclusive":true,"is_archived":false}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "edit", "3", "-R", "owner/repo", "--exclusive=false", "--archived", fields}))
	require.JSONEq(t, `{"id":3,"name":"scoped","color":"00aa00","exclusive":false,"is_archived":true}`, out.String())
	require.Equal(t, 2, calls)
	require.Empty(t, diagnostics.String())
}
