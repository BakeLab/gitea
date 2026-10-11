// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotificationBoundsShareClockAndReachServer(t *testing.T) {
	now := time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC)
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/notifications", r.URL.Path)
		require.Equal(t, "2028-01-31T12:00:00Z", r.URL.Query().Get("since"))
		require.Equal(t, "2028-02-01T11:00:00Z", r.URL.Query().Get("before"))
		require.Equal(t, []string{"unread", "pinned"}, r.URL.Query()["status-types"])
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	reads := 0
	deps.Now = func() time.Time { reads++; return now }
	err := Execute(context.Background(), deps, []string{"notification", "list", "--mine", "--since=24h", "--before=1h", "--json"})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Equal(t, 1, calls)
	require.JSONEq(t, `[]`, out.String())
}
