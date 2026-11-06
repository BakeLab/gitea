// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaTagLifecycleAndProtection(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-tag-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName)...) }
	fails := func(args ...string) error {
		out.Reset()
		return Execute(context.Background(), deps, append(args, "-R", repo.FullName, "--no-input"))
	}
	head := strings.TrimSpace(string(scoped("api", "repos/"+repo.FullName+"/branches/"+repo.DefaultBranch, "--jq", ".commit.id")))
	// Another commit, so that a tag can point away from the head.
	deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":%q,"content":"dGVzdAo=","message":"Second commit"}`, repo.DefaultBranch))
	execute("api", "repos/"+repo.FullName+"/contents/second.txt", "--input", "-")
	second := strings.TrimSpace(string(scoped("api", "repos/"+repo.FullName+"/branches/"+repo.DefaultBranch, "--jq", ".commit.id")))
	require.NotEqual(t, head, second)

	t.Run("create, list and view", func(t *testing.T) {
		var light services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "create", "v0.1.0", "--target", head, "--json"), &light))
		require.Equal(t, "v0.1.0", light.Name)
		require.Equal(t, head, light.Commit)
		require.False(t, light.Annotated)
		require.Empty(t, light.Message, "Gitea answers the commit message, which is not the tag's")

		var annotated services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "create", "v1.0.0", "--message", "Version 1.0.0\n\nThe notes.", "--json"), &annotated))
		require.Equal(t, second, annotated.Commit, "a tag with no target points to the head of the default branch")
		require.True(t, annotated.Annotated)
		require.NotEqual(t, annotated.Commit, annotated.ID, "an annotated tag is an object of its own")

		var tags []services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "list", "--json", "name,commit,annotated"), &tags))
		require.Len(t, tags, 2)
		byName := map[string]services.Tag{}
		for _, tag := range tags {
			byName[tag.Name] = tag
		}
		require.Equal(t, head, byName["v0.1.0"].Commit)
		require.True(t, byName["v1.0.0"].Annotated)
		require.Contains(t, string(scoped("tag", "list")), "v1.0.0\t"+second+"\tannotated\tVersion 1.0.0", "piped, the full SHA comes first")

		var viewed services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "view", "v1.0.0", "--json"), &viewed))
		require.NotEmpty(t, viewed.Tagger)
		require.NotEmpty(t, viewed.TaggedAt)
		require.Contains(t, viewed.Message, "The notes.")
		require.Contains(t, viewed.TarballURL, "v1.0.0")
		text := string(scoped("tag", "view", "v1.0.0"))
		require.Contains(t, text, "tagger:\t"+viewed.Tagger)
		require.Contains(t, string(scoped("tag", "view", "v0.1.0")), "commit:\t"+head)
		require.NotContains(t, string(scoped("tag", "view", "v0.1.0")), "tagger", "a lightweight tag has none")

		require.ErrorContains(t, fails("tag", "view", "no-such-tag"), `tag "no-such-tag"`)
		require.Error(t, fails("tag", "create", "v1.0.0"), "a tag that exists cannot be created again")
		require.ErrorContains(t, fails("tag", "create", "bad..name"), "invalid tag name")
		require.ErrorContains(t, fails("tag", "create", "v9", "--target", "no-such-ref"), `target "no-such-ref" not found`)
	})

	t.Run("a release uses the tag", func(t *testing.T) {
		scoped("release", "create", "v1.0.0", "--title", "One", "--notes", "n", "--verify-tag")
		require.Equal(t, "v1.0.0", strings.TrimSpace(string(scoped("release", "view", "v1.0.0", "--json", "tag", "--jq", ".tag"))))
	})

	t.Run("protection", func(t *testing.T) {
		require.Equal(t, "[]\n", string(scoped("tag", "protection", "list", "--json")))
		scoped("tag", "protect", "v*", "--allow-user", "integration")
		var rules []services.TagProtection
		require.NoError(t, json.Unmarshal(scoped("tag", "protection", "list", "--json"), &rules))
		require.Len(t, rules, 1)
		require.Equal(t, "v*", rules[0].NamePattern)
		require.Equal(t, []string{"integration"}, rules[0].Users)
		require.Empty(t, rules[0].Teams)
		require.Equal(t, "v*\tintegration\t\n", string(scoped("tag", "protection", "list")))

		// Protecting a pattern again changes only the list that is named.
		scoped("tag", "protect", "v*", "--allow-user", "integration")
		require.NoError(t, json.Unmarshal(scoped("tag", "protection", "list", "--json"), &rules))
		require.Len(t, rules, 1, "the rule of the pattern is changed, not added again")
		diagnostics.Reset()
		require.NoError(t, fails("tag", "protect", "v*", "--allow-user="))
		require.Contains(t, diagnostics.String(), "nobody can create matching tags")
		require.NoError(t, json.Unmarshal(scoped("tag", "protection", "list", "--json"), &rules))
		require.Empty(t, rules[0].Users, "an empty --allow-user= takes every user out of an existing rule")
		scoped("tag", "protect", "v*", "--allow-user", "integration")
		require.ErrorContains(t, fails("tag", "protect", "release-*"), "tag protect requires --allow-user or --allow-team")
		require.ErrorContains(t, fails("tag", "protect", "release-*", "--allow-user="), "needs at least one user or team", "a new rule says who may create the tags")
		require.ErrorContains(t, fails("tag", "protect", "release-*", "--allow-team", "core"), "--allow-team requires a repository owned by an organization", "Gitea would drop the team")
		scoped("tag", "protect", "release-*", "--allow-user", "integration")
		require.NoError(t, json.Unmarshal(scoped("tag", "protection", "list", "--json"), &rules))
		require.Len(t, rules, 2)

		scoped("tag", "unprotect", "release-*", "--yes")
		require.Error(t, fails("tag", "unprotect", "release-*", "--yes"), "a rule that is gone cannot be removed again")
		require.Error(t, fails("tag", "unprotect", "v*"), "without --yes and a terminal there is nobody to ask")
		scoped("tag", "unprotect", "v*", "--yes")
		require.Equal(t, "[]\n", string(scoped("tag", "protection", "list", "--json")))
	})

	t.Run("delete", func(t *testing.T) {
		require.Error(t, fails("tag", "delete", "v0.1.0"), "without --yes and a terminal there is nobody to ask")
		scoped("tag", "delete", "v0.1.0", "--yes")
		require.ErrorContains(t, fails("tag", "view", "v0.1.0"), "not found")
		require.ErrorContains(t, fails("tag", "delete", "v0.1.0", "--yes"), "v0.1.0")
		require.ErrorContains(t, fails("tag", "delete", "v1.0.0", "--yes"), "gt release delete v1.0.0 --delete-tag", "a tag of a release says how to delete both")
		scoped("release", "delete", "v1.0.0", "--yes")
		var tags []services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "list", "--json", "name"), &tags))
		require.Equal(t, []services.Tag{{Name: "v1.0.0"}}, tags)
		scoped("tag", "delete", "v1.0.0", "--yes")
		require.Equal(t, "[]\n", string(scoped("tag", "list", "--json")))
	})

	t.Run("dry run", func(t *testing.T) {
		scoped("tag", "create", "keep-me")
		require.Contains(t, string(scoped("tag", "delete", "keep-me", "--dry-run")), "Dry run: would send DELETE ")
		require.Contains(t, string(scoped("tag", "create", "never", "--dry-run")), "Dry run: would send POST ")
		require.Contains(t, string(scoped("tag", "protect", "x*", "--allow-user", "integration", "--dry-run")), "Dry run: would send POST ")
		var tags []services.Tag
		require.NoError(t, json.Unmarshal(scoped("tag", "list", "--json", "name"), &tags))
		require.Equal(t, []services.Tag{{Name: "keep-me"}}, tags, "nothing was created or deleted")
		require.Equal(t, "[]\n", string(scoped("tag", "protection", "list", "--json")))
	})
}
