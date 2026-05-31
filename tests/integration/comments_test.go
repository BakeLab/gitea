// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommentsCommandLifecycle exercises `tea comments add`, `list`, `edit`,
// and `delete` end-to-end against a live Gitea instance. It creates a throwaway
// repo with one issue, then drives the comment subcommands and verifies state
// via the SDK after each step.
func TestCommentsCommandLifecycle(t *testing.T) {
	login := createIntegrationLogin(t)
	client := login.Client()
	ctx := context.Background()

	repoName := fmt.Sprintf("tea-comments-integration-%d", time.Now().UnixNano())
	_, _, err := client.CreateRepo(ctx, gitea.CreateRepoOption{Name: repoName, AutoInit: true})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.DeleteRepo(ctx, login.User, repoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", repoName, delErr)
		}
	})

	repoSlug := fmt.Sprintf("%s/%s", login.User, repoName)

	issue, _, err := client.Issues.CreateIssue(ctx, login.User, repoName, gitea.CreateIssueOption{
		Title: "comment test issue",
		Body:  "scratch issue for tea comment integration tests",
	})
	require.NoError(t, err)
	issueIdx := strconv.FormatInt(issue.Index, 10)

	// add via positional body
	addOutput := runTeaCommand(t,
		"comments", "add",
		"--login", login.Name,
		"--repo", repoSlug,
		issueIdx, "first comment",
	)
	assert.Contains(t, addOutput, "first comment")

	// add via the historical shorthand (parent command, no "add" subcommand)
	runTeaCommand(t,
		"comments",
		"--login", login.Name,
		"--repo", repoSlug,
		issueIdx, "second comment",
	)

	// list should now show both, in tabular form with IDs
	listOutput := runTeaCommand(t,
		"comments", "list",
		"--login", login.Name,
		"--repo", repoSlug,
		issueIdx,
	)
	assert.Contains(t, listOutput, "first comment")
	assert.Contains(t, listOutput, "second comment")

	// pull the real IDs from the server so edit/delete have something to act on
	apiComments, _, err := client.Issues.ListIssueComments(ctx, login.User, repoName, issue.Index, gitea.ListIssueCommentOptions{})
	require.NoError(t, err)
	require.Len(t, apiComments, 2)

	var firstID, secondID int64
	for _, c := range apiComments {
		switch {
		case strings.Contains(c.Body, "first comment"):
			firstID = c.ID
		case strings.Contains(c.Body, "second comment"):
			secondID = c.ID
		}
	}
	require.NotZero(t, firstID, "first comment id not found in listing")
	require.NotZero(t, secondID, "second comment id not found in listing")

	// edit the first comment via positional body
	editOutput := runTeaCommand(t,
		"comments", "edit",
		"--login", login.Name,
		"--repo", repoSlug,
		strconv.FormatInt(firstID, 10), "first comment (edited)",
	)
	assert.Contains(t, editOutput, "first comment (edited)")

	edited, _, err := client.Issues.GetIssueComment(ctx, login.User, repoName, firstID)
	require.NoError(t, err)
	assert.Equal(t, "first comment (edited)", edited.Body)

	// delete both comments in one batch
	deleteOutput := runTeaCommand(t,
		"comments", "delete",
		"--login", login.Name,
		"--repo", repoSlug,
		strconv.FormatInt(firstID, 10),
		strconv.FormatInt(secondID, 10),
	)
	assert.Contains(t, deleteOutput, fmt.Sprintf("Deleted comment %d", firstID))
	assert.Contains(t, deleteOutput, fmt.Sprintf("Deleted comment %d", secondID))

	remaining, _, err := client.Issues.ListIssueComments(ctx, login.User, repoName, issue.Index, gitea.ListIssueCommentOptions{})
	require.NoError(t, err)
	assert.Empty(t, remaining, "expected all comments to be deleted")
}
