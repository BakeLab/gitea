// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

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
	require.Equal(t, "1  alice/project#3  alice  1h25m0s  2026-10-01T10:00:00Z\n"+
		"2  bob/tools#3      alice  15m0s    2026-10-02T10:00:00Z\n"+
		"Total on this page: 1h40m0s\n", out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"time", "list", "--mine", "--json=repository,issue_number"}))
	require.JSONEq(t, `[{"repository":"alice/project","issue_number":3},{"repository":"bob/tools","issue_number":3}]`, out.String())
}
