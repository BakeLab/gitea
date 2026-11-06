// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGiteaIssueLockPinAndDelete(t *testing.T) {
	repo := newIntegrationRepo(t, "gt-issue-actions")
	first, second, third := repo.issue(t, "Locked"), repo.issue(t, "Pinned"), repo.issue(t, "Deleted")

	repo.execute("issue", "lock", first, "--reason", "Resolved", "-R", repo.FullName)
	require.Equal(t, "true", repo.api("issues/"+first, ".is_locked"))
	require.Contains(t, repo.diagnosed(t, "issue", "lock", first), "is already locked", "a locked issue is reported, not locked again")
	require.Equal(t, "true", repo.api("issues/"+first, ".is_locked"))
	repo.execute("issue", "unlock", first, "-R", repo.FullName)
	require.Equal(t, "false", repo.api("issues/"+first, ".is_locked"))

	repo.execute("issue", "lock", first, second, "-R", repo.FullName)
	require.Equal(t, "true", repo.api("issues/"+second, ".is_locked"), "several issues are locked at once")
	require.JSONEq(t, `{"number":`+second+`}`, string(repo.execute("issue", "unlock", second, "-R", repo.FullName, "--json", "number")), "the issue comes back as JSON")

	repo.execute("issue", "pin", second, "-R", repo.FullName)
	require.Equal(t, second, repo.api("issues/pinned", ".[0].number"))
	repo.execute("issue", "unpin", second, "-R", repo.FullName)
	require.Equal(t, "0", repo.api("issues/pinned", "length"))

	err := repo.fails(t, "issue", "delete", third)
	require.Error(t, err, "without --yes and a terminal there is nobody to ask")
	require.Equal(t, "Deleted", repo.api("issues/"+third, ".title"), "nothing was deleted")
	repo.execute("issue", "delete", third, "-R", repo.FullName, "--yes")
	require.ErrorContains(t, repo.fails(t, "issue", "view", third), "not found")
	repo.execute("issue", "delete", first, second, "-R", repo.FullName, "--yes")
	require.ErrorContains(t, repo.fails(t, "issue", "view", first), "not found")
	require.ErrorContains(t, repo.fails(t, "issue", "view", second), "not found")
}
