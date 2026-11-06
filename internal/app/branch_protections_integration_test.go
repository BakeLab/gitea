// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaBranchProtections(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-protections-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName)...) }
	rules := func() (list []services.BranchRule) {
		t.Helper()
		require.NoError(t, json.Unmarshal(scoped("branch", "protection", "list", "--json"), &list))
		return list
	}
	byName := func(name string) services.BranchRule {
		t.Helper()
		for _, rule := range rules() {
			if rule.Rule == name {
				return rule
			}
		}
		t.Fatalf("no rule %q", name)
		return services.BranchRule{}
	}
	scoped("branch", "create", "release/1.0")

	require.Equal(t, "[]\n", string(scoped("branch", "protection", "list", "--json")), "a new repository has no rules")

	scoped("branch", "protect", repo.DefaultBranch, "--required-approvals", "2", "--status-check", "ci/build,ci/test")
	scoped("branch", "protect", "--pattern", "release/*", "--allow-push", "integration", "--block-on-rejected-reviews", "--dismiss-stale-approvals")
	list := rules()
	require.Len(t, list, 2)
	require.Equal(t, repo.DefaultBranch, list[0].Rule, "in the order that Gitea applies them")
	require.Equal(t, "release/*", list[1].Rule)

	main := byName(repo.DefaultBranch)
	require.EqualValues(t, 2, main.RequiredApprovals)
	require.True(t, main.StatusCheckRequired)
	require.Equal(t, []string{"ci/build", "ci/test"}, main.StatusChecks)
	require.False(t, main.PushEnabled, "nobody may push directly unless --allow-push says so")
	require.NotEmpty(t, main.CreatedAt)

	release := byName("release/*")
	require.True(t, release.PushEnabled)
	require.True(t, release.PushRestricted)
	require.Equal(t, []string{"integration"}, release.PushUsers)
	require.Empty(t, release.PushTeams)
	require.True(t, release.BlockOnRejected)
	require.True(t, release.DismissStaleApprovals)
	require.False(t, release.StatusCheckRequired)
	require.Empty(t, release.StatusChecks)

	text := string(scoped("branch", "protection", "list"))
	require.Contains(t, text, repo.DefaultBranch+"\t2\tnobody\tci/build, ci/test\t")
	require.Contains(t, text, "release/*\t1\tintegration\t\tblocks on rejected reviews, dismisses stale approvals")

	var viewed services.BranchRule
	require.NoError(t, json.Unmarshal(scoped("branch", "protection", "view", "release/*", "--json"), &viewed))
	require.Equal(t, release, viewed, "view shows the rule that the list shows")
	view := string(scoped("branch", "protection", "view", repo.DefaultBranch))
	for _, want := range []string{"title:\t" + repo.DefaultBranch, "required approvals:\t2", "may push:\tnobody", "status checks:\tci/build, ci/test"} {
		require.Contains(t, view, want)
	}
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"branch", "protection", "view", "no-such-rule", "-R", repo.FullName}), `protection rule "no-such-rule"`)

	// What branch view says agrees with the rules.
	var branch services.Branch
	require.NoError(t, json.Unmarshal(scoped("branch", "view", "release/1.0", "--json"), &branch))
	require.True(t, branch.Protected, "the pattern protects the branches it matches")

	scoped("branch", "protect", repo.DefaultBranch, "--required-approvals", "3")
	require.EqualValues(t, 3, byName(repo.DefaultBranch).RequiredApprovals, "a change shows in the list")
	require.Equal(t, []string{"ci/build", "ci/test"}, byName(repo.DefaultBranch).StatusChecks, "and keeps what it did not name")
	scoped("branch", "protect", repo.DefaultBranch, "--status-check=")
	require.False(t, byName(repo.DefaultBranch).StatusCheckRequired, "an empty --status-check= stops requiring checks")
	require.Empty(t, byName(repo.DefaultBranch).StatusChecks)

	scoped("branch", "unprotect", "release/*", "--yes")
	list = rules()
	require.Len(t, list, 1)
	require.Equal(t, repo.DefaultBranch, list[0].Rule)
	scoped("branch", "unprotect", repo.DefaultBranch, "--yes")
	require.Equal(t, "[]\n", string(scoped("branch", "protection", "list", "--json")))

	// A branch is protected by the first rule in the list that matches it, as the help of the list says.
	scoped("branch", "protect", "--pattern", "release/*", "--required-approvals", "1")
	scoped("branch", "protect", "release/1.0", "--required-approvals", "5")
	list = rules()
	require.Equal(t, []string{"release/*", "release/1.0"}, []string{list[0].Rule, list[1].Rule})
	require.NoError(t, json.Unmarshal(scoped("branch", "view", "release/1.0", "--json"), &branch))
	require.EqualValues(t, 1, branch.RequiredApprovals, "the pattern rule comes first, so its approvals apply")
	scoped("branch", "unprotect", "release/*", "release/1.0", "--yes")

	// Gitea answers every rule at once, so many rules need no pages.
	for i := range 3 {
		scoped("branch", "protect", "--pattern", fmt.Sprintf("topic-%d/*", i))
	}
	require.Len(t, rules(), 3)
}
