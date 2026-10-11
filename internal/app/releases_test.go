// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseCreateWithTagFlagReturnsSingleJSONObject(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/releases", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","name":"v1"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "--tag", "v1", "-R", "alice/project", "--json=id,tag"}))
	require.JSONEq(t, `{"id":7,"tag":"v1"}`, out.String())
}
