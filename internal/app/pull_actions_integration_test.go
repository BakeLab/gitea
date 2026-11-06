// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGiteaPullLockPinAndDelete(t *testing.T) {
	repo := newIntegrationRepo(t, "gt-pr-actions")
	n := repo.pull(t, "feature", "Actions")
	issue := repo.issue(t, "An issue")

	repo.execute("pr", "lock", n, "--reason", "Resolved", "-R", repo.FullName)
	require.Equal(t, "true", repo.api("issues/"+n, ".is_locked"))
	require.Contains(t, repo.diagnosed(t, "pr", "lock", n), "is already locked")
	repo.execute("pr", "unlock", n, "-R", repo.FullName)
	require.Equal(t, "false", repo.api("issues/"+n, ".is_locked"))

	repo.execute("pr", "pin", n, "-R", repo.FullName)
	require.Equal(t, n, repo.api("pulls/pinned", ".[0].number"), "Gitea lists the pinned pull requests")
	repo.execute("pr", "unpin", n, "-R", repo.FullName)
	require.Equal(t, "0", repo.api("pulls/pinned", "length"))

	for _, verb := range []string{"lock", "unlock", "pin", "unpin", "delete"} {
		require.ErrorContains(t, repo.fails(t, "pr", verb, issue, "--yes"), "is an issue, not a pull request", verb)
	}
	require.ErrorContains(t, repo.fails(t, "issue", "delete", n, "--yes"), "is a pull request; use gt pr delete")
	require.Equal(t, "An issue", repo.api("issues/"+issue, ".title"), "no refused command deleted anything")

	require.Error(t, repo.fails(t, "pr", "delete", n), "without --yes and a terminal there is nobody to ask")
	require.Equal(t, "Actions", repo.api("pulls/"+n, ".title"))
	repo.execute("pr", "delete", n, "-R", repo.FullName, "--yes")
	require.ErrorContains(t, repo.fails(t, "pr", "view", n), "not found")
	require.Equal(t, "feature", repo.api("branches/feature", ".name"), "the head branch stays")
}

func TestGiteaDependenciesOfIssuesAndPullRequests(t *testing.T) {
	repo := newIntegrationRepo(t, "gt-dependencies")
	first, second, third := repo.issue(t, "First"), repo.issue(t, "Second"), repo.issue(t, "Third")
	pull := repo.pull(t, "feature", "The pull request")
	numbers := func(path string) string { return repo.api(path, "[.[].number] | sort | map(tostring) | join(\",\")") }

	repo.execute("issue", "edit", first, "--add-blocked-by", second, "--add-blocks", third, "-R", repo.FullName)
	require.Equal(t, second, numbers("issues/"+first+"/dependencies"), "the first issue waits for the second")
	require.Equal(t, third, numbers("issues/"+first+"/blocks"), "and blocks the third")
	require.Equal(t, first, numbers("issues/"+third+"/dependencies"), "which waits for the first")
	require.JSONEq(t, `{"blocked_by":[{"repository":"`+repo.FullName+`","number":`+second+`}],"blocks":[{"repository":"`+repo.FullName+`","number":`+third+`}]}`,
		string(repo.execute("issue", "view", first, "-R", repo.FullName, "--json", "blocked_by,blocks")))

	require.ErrorContains(t, repo.fails(t, "issue", "edit", first, "--add-blocked-by", third, "--add-blocks", third), "cannot both block and be blocked by the edited issue")
	require.ErrorContains(t, repo.fails(t, "issue", "edit", first, "--add-blocks", first), "an issue cannot block itself")
	require.Error(t, repo.fails(t, "issue", "edit", third, "--add-blocks", first), "Gitea refuses a dependency cycle")

	repo.execute("issue", "edit", first, "--remove-blocked-by", second, "--remove-blocks", third, "-R", repo.FullName)
	require.Empty(t, numbers("issues/"+first+"/dependencies"))
	require.Empty(t, numbers("issues/"+first+"/blocks"))
	repo.execute("issue", "edit", first, "--remove-blocks", third, "-R", repo.FullName) // lifting what is not there changes nothing

	require.JSONEq(t, `{"blocked_by":[{"repository":"`+repo.FullName+`","number":`+first+`}],"blocks":[{"repository":"`+repo.FullName+`","number":`+second+`}]}`,
		string(repo.execute("pr", "edit", pull, "--add-blocked-by", first, "--add-blocks", second, "-R", repo.FullName, "--json", "blocked_by,blocks")), "an edit reports the dependencies too")
	require.Equal(t, first, numbers("issues/"+pull+"/dependencies"), "a pull request waits for an issue")
	require.Equal(t, second, numbers("issues/"+pull+"/blocks"), "and blocks one")
	require.JSONEq(t, `{"number":`+pull+`,"blocked_by":[{"repository":"`+repo.FullName+`","number":`+first+`}],"blocks":[{"repository":"`+repo.FullName+`","number":`+second+`}]}`,
		string(repo.execute("pr", "view", pull, "-R", repo.FullName, "--json", "number,blocked_by,blocks")))
	require.JSONEq(t, `{"number":`+pull+`}`, string(repo.execute("pr", "view", pull, "-R", repo.FullName, "--json", "number")))

	repo.execute("pr", "edit", pull, "--remove-blocked-by", first, "--remove-blocks", second, "-R", repo.FullName)
	require.Empty(t, numbers("issues/"+pull+"/dependencies"))
	require.Empty(t, numbers("issues/"+pull+"/blocks"))
	require.ErrorContains(t, repo.fails(t, "pr", "edit", pull, "--add-blocks", pull), "names the edited issue")
}
