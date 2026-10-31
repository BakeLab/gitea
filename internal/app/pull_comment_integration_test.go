// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaPullStateCommentsAndUpdate(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-pr-state-%d", time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	scoped := func(args ...string) []byte { return execute(append(args, "-R", repo.FullName, "--json")...) }
	commit := func(branch, path, message string) {
		deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":%q,"content":"dGVzdAo=","message":%q}`, branch, message))
		execute("api", "repos/"+repo.FullName+"/contents/"+path, "--input", "-")
	}
	execute("branch", "create", "feature", "-R", repo.FullName)
	commit("feature", "feature.txt", "Feature change")

	var pull services.Pull
	require.NoError(t, json.Unmarshal(scoped("pr", "create", "--title", "State changes", "--body", "Body", "--head", "feature", "--base", repo.DefaultBranch, "--draft"), &pull))
	require.True(t, pull.Draft)
	n := strconv.FormatInt(pull.Number, 10)
	execute("pr", "ready", n, "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("pr", "view", n), &pull))
	require.False(t, pull.Draft)
	execute("pr", "ready", n, "--undo", "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("pr", "view", n), &pull))
	require.True(t, pull.Draft)
	require.Equal(t, "feature.txt\n", string(execute("pr", "diff", n, "--name-only", "-R", repo.FullName)))
	diff := string(execute("pr", "diff", n, "--color", "never", "-R", repo.FullName))
	require.Contains(t, diff, "diff --git a/feature.txt b/feature.txt")
	require.Contains(t, diff, "+test")

	var comment services.Comment
	require.NoError(t, json.Unmarshal(scoped("pr", "comment", "create", n, "--body", "First"), &comment))
	require.Equal(t, pull.Number, comment.IssueNumber)
	commentID := strconv.FormatInt(comment.ID, 10)
	require.NoError(t, json.Unmarshal(scoped("pr", "comment", "edit", commentID, "--body", "Edited"), &comment))
	require.Equal(t, "Edited", comment.Body)
	require.NoError(t, json.Unmarshal(scoped("pr", "comment", "view", commentID), &comment))
	require.Equal(t, "Edited", comment.Body)
	var comments []services.Comment
	require.NoError(t, json.Unmarshal(scoped("pr", "comment", "list", n), &comments))
	require.Equal(t, []services.Comment{comment}, comments)
	execute("pr", "comment", "delete", commentID, "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(scoped("pr", "comment", "list", n)))

	execute("pr", "close", n, "-c", "Pausing", "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("pr", "view", n), &pull))
	require.Equal(t, "closed", pull.State)
	execute("pr", "reopen", n, "-R", repo.FullName)
	require.NoError(t, json.Unmarshal(scoped("pr", "view", n), &pull))
	require.Equal(t, "open", pull.State)
	var events []services.TimelineEvent
	require.NoError(t, json.Unmarshal(scoped("pr", "timeline", n), &events))
	var types []string
	for _, event := range events {
		if event.Type != "pull_push" {
			types = append(types, event.Type)
		}
	}
	require.Equal(t, []string{"comment", "close", "reopen"}, types[len(types)-3:])

	commit(repo.DefaultBranch, "base.txt", "Base change")
	execute("pr", "update-branch", n, "-R", repo.FullName)
	var file struct{ Path string }
	require.NoError(t, json.Unmarshal(execute("api", "repos/"+repo.FullName+"/contents/base.txt?ref=feature"), &file))
	require.Equal(t, "base.txt", file.Path, "the head branch has the base commit")
}
