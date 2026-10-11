// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryEditPreservesExplicitClearsAndFalse(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project", r.URL.Path)
		requirePayload(t, `{"name":"renamed","website":"","default_branch":"main","archived":false,"private":false,"template":false,"has_actions":false}`, r.Body)
		_, _ = w.Write([]byte(`{"name":"renamed","full_name":"alice/renamed","website":"","default_branch":"main"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "-R", "alice/project", "--name", "renamed", "--website=", "--default-branch", "main", "--archived=false", "--private=false", "--as-template=false", "--actions=false", "--json=name,website,default_branch"}))
	require.JSONEq(t, `{"name":"renamed","website":"","default_branch":"main"}`, out.String())
	require.Equal(t, 1, calls)
}
