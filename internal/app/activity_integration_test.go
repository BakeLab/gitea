// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

// TestGiteaActivityFeeds runs as a user of its own. Gitea 28 does not limit a feed to readable
// repositories when its own user or a site admin reads it, and answers 500 for an entry of a deleted
// repository, which a push still being recorded when a test deletes its repository leaves behind.
func TestGiteaActivityFeeds(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	user := fmt.Sprintf("gt-activity-%d", time.Now().UnixNano())
	execute("admin", "user", "create", user, "--email", user+"@example.test", "--password", "gt-activity-pass-1", "--must-change-password=false")
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"admin", "user", "delete", user, "--purge", "--yes"}); err != nil {
			t.Errorf("user cleanup: %v", err)
		}
	})
	as := func(args ...string) []byte { return execute(append(args, "--sudo", user)...) }
	var repo services.Repository
	require.NoError(t, json.Unmarshal(as("repo", "create", "activity", "--auto-init", "--private", "--json"), &repo))
	scoped := func(args ...string) []byte { return as(append(args, "-R", repo.FullName)...) }
	var issue services.Issue
	require.NoError(t, json.Unmarshal(scoped("issue", "create", "--title", "Crash on start", "--body", "b", "--json"), &issue))
	scoped("issue", "comment", "create", fmt.Sprint(issue.Number), "--body", "Looks good to me")
	scoped("issue", "close", fmt.Sprint(issue.Number))
	scoped("branch", "create", "feature")
	deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":"feature","content":%q,"message":"Add the feature"}`, base64.StdEncoding.EncodeToString([]byte("x\n"))))
	as("api", "repos/"+repo.FullName+"/contents/feature.txt", "--input", "-")
	var pull services.Pull
	require.NoError(t, json.Unmarshal(scoped("pr", "create", "--title", "The feature", "--body", "b", "--head", "feature", "--base", repo.DefaultBranch, "--json"), &pull))
	scoped("pr", "merge", fmt.Sprint(pull.Number), "--squash", "--yes")
	scoped("release", "create", "v1", "--title", "One", "--notes", "n")

	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), deps, append(args, "--sudo", user, "--no-input")))
		return out.String()
	}
	types := func(args ...string) []string {
		t.Helper()
		return strings.Split(strings.TrimSpace(run(append([]string{"repo", "activity", "-R", repo.FullName, "--limit", "100", "--json", "type", "--jq", `[.[].type] | join(",")`}, args...)...)), ",")
	}

	// Pushes are recorded in the background, so the feed is read once it has stopped growing.
	all := types()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		time.Sleep(500 * time.Millisecond)
		again := types()
		if len(again) == len(all) {
			break
		}
		all = again
	}
	for _, want := range []string{"create_repo", "create_issue", "comment_issue", "close_issue", "commit_repo", "create_pull_request", "merge_pull_request", "publish_release"} {
		require.Contains(t, all, want)
	}
	newest := strings.TrimSpace(run("repo", "activity", "-R", repo.FullName, "--limit", "1", "--json", "created_at", "--jq", ".[0].created_at"))
	today := newest[:len(time.DateOnly)] // the time zone of --date is the server's, as is that of created_at
	require.ElementsMatch(t, all, types("--date", today), "everything happened today")
	require.Equal(t, []string{""}, types("--date", "2000-01-01"), "and nothing in 2000")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"repo", "activity", "-R", repo.FullName, "--date", "today", "--sudo", user}), "--date requires a day as YYYY-MM-DD")

	var feed []services.Activity
	require.NoError(t, json.Unmarshal([]byte(run("repo", "activity", "-R", repo.FullName, "--limit", "100", "--json")), &feed))
	times := make([]string, len(feed))
	byType := map[string]services.Activity{}
	for i, entry := range feed {
		times[i], byType[entry.Type] = entry.CreatedAt, entry
	}
	require.True(t, slices.IsSortedFunc(times, func(a, b string) int { return strings.Compare(b, a) }), "newest first: %v", times)
	require.Equal(t, user, byType["create_issue"].Actor)
	require.Equal(t, repo.FullName, byType["create_issue"].Repository)
	require.Equal(t, fmt.Sprintf("%d|Crash on start", issue.Number), byType["create_issue"].Content)
	require.Equal(t, fmt.Sprintf("%d|Looks good to me", issue.Number), byType["comment_issue"].Content)
	require.Positive(t, byType["comment_issue"].CommentID)
	require.True(t, byType["create_issue"].Private, "the activity of a private repository says so")
	require.Equal(t, "refs/tags/v1", byType["publish_release"].Ref)
	require.NotEmpty(t, byType["create_issue"].CreatedAt)

	text := run("repo", "activity", "-R", repo.FullName, "--limit", "100")
	require.Contains(t, text, fmt.Sprintf("%s\topened issue\t%s\t#%d Crash on start", user, repo.FullName, issue.Number))
	require.Contains(t, text, fmt.Sprintf("commented on issue\t%s\t#%d: Looks good to me", repo.FullName, issue.Number))
	require.Contains(t, text, "closed issue")
	require.Contains(t, text, "published the release\t"+repo.FullName+"\tOne (v1)")
	require.Contains(t, text, "merged pull request")

	var second []services.Activity
	require.NoError(t, json.Unmarshal([]byte(run("repo", "activity", "-R", repo.FullName, "--limit", "3", "--page", "2", "--json", "id")), &second))
	require.Len(t, second, 3, "the second page of three")
	for _, entry := range second {
		require.False(t, slices.ContainsFunc(feed[:3], func(first services.Activity) bool { return first.ID == entry.ID }), "and not the first")
	}

	// The feed of the account holds this repository's activity; --only-performed-by keeps what the user did.
	own := func(args ...string) (entries []services.Activity) {
		t.Helper()
		require.NoError(t, json.Unmarshal([]byte(run(append([]string{"activity", "--limit", "100", "--json"}, args...)...)), &entries))
		return entries
	}
	ids := func(entries []services.Activity) (list []int64) {
		for _, entry := range entries {
			list = append(list, entry.ID)
		}
		return list
	}
	ownFeed, performed := own(), own("--only-performed-by")
	require.Contains(t, ids(ownFeed), byType["create_issue"].ID)
	require.Contains(t, ids(performed), byType["create_issue"].ID)
	require.Equal(t, ids(ownFeed), ids(own(user)), "naming yourself is the same feed")
	require.Equal(t, ids(own("--date", today)), ids(ownFeed), "and everything happened today")

	// An organization has a feed of its own.
	org := user + "-org"
	as("org", "create", org)
	var orgRepo services.Repository
	require.NoError(t, json.Unmarshal(as("repo", "create", org+"/inside", "--auto-init", "--json"), &orgRepo))
	orgFeed := own(org)
	require.NotEmpty(t, orgFeed)
	require.Equal(t, orgRepo.FullName, orgFeed[0].Repository)
	require.Equal(t, "create_repo", orgFeed[0].Type)
	require.NotContains(t, ids(orgFeed), byType["create_issue"].ID, "the activity of another owner is not in it")

	require.ErrorContains(t, Execute(context.Background(), deps, []string{"activity", "gt-no-such-owner-at-all", "--no-input", "--sudo", user}), `"gt-no-such-owner-at-all"`)
}
