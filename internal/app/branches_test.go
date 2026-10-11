// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchNames(t *testing.T) {
	for _, name := range []string{"main", "feature/next", "release-1.0", "topic.locked"} {
		require.NoError(t, validateBranch(name), name)
	}
	for _, name := range []string{"", "@", "-flag", "/root", "trailing/", "trailing.", "two..dots", "ref@{1}", "a//b", "a/.hidden", "topic.lock", "space here", "bad\x00ref", "bad\x7fref"} {
		require.Error(t, validateBranch(name), name)
	}
}

func TestBulkBranchProtectionContinuesAfterFailure(t *testing.T) {
	var names []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/owner/repo/branch_protections/")
		names = append(names, name)
		if name == "failed" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"branch", "unprotect", "failed", "succeeded", "-R", "owner/repo", "--no-input"})
	require.Error(t, err)
	require.Empty(t, names)
	err = Execute(context.Background(), deps, []string{"branch", "unprotect", "failed", "succeeded", "-R", "owner/repo", "--yes"})
	require.ErrorContains(t, err, "failed")
	require.Equal(t, []string{"failed", "succeeded"}, names)
	require.Empty(t, out.String())
	require.Contains(t, diagnostics.String(), "succeeded")
}
