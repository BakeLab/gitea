// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/tea/cmd/pulls"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestPullsReply(t *testing.T) {
	login := createIntegrationLogin(t)
	client := login.Client()
	timestamp := time.Now().UnixNano()
	repoName := fmt.Sprintf("tea-pr-reply-%d", timestamp)
	featureBranch := fmt.Sprintf("reply-test-%d", timestamp)
	replyBody := fmt.Sprintf("Thanks for the review %d", timestamp)

	repo, _, err := client.Repositories.CreateRepo(t.Context(), gitea.CreateRepoOption{
		Name:          repoName,
		AutoInit:      true,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, delErr := client.Repositories.DeleteRepo(t.Context(), login.User, repoName); delErr != nil {
			t.Logf("failed to delete integration test repo %q: %v", repoName, delErr)
		}
	})

	baseBranch := repo.DefaultBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	_, _, err = client.Repositories.CreateFile(t.Context(), login.User, repoName, "review.txt", gitea.CreateFileOptions{
		FileOptions: gitea.FileOptions{
			Message:       "add review target",
			BranchName:    baseBranch,
			NewBranchName: featureBranch,
		},
		Content: base64.StdEncoding.EncodeToString([]byte("line for review\n")),
	})
	require.NoError(t, err)

	pr, _, err := client.PullRequests.CreatePullRequest(t.Context(), login.User, repoName, gitea.CreatePullRequestOption{
		Base:  baseBranch,
		Head:  featureBranch,
		Title: "Integration test for pr reply",
		Body:  "Adds a file so we can reply to a review comment.",
	})
	require.NoError(t, err)

	review, _, err := client.PullRequests.CreatePullReview(t.Context(), login.User, repoName, pr.Index, gitea.CreatePullReviewOptions{
		State: gitea.ReviewStateComment,
		Body:  "Please take another look.",
		Comments: []gitea.CreatePullReviewComment{{
			Path:       "review.txt",
			Body:       "Could you clarify this line?",
			NewLineNum: 1,
		}},
	})
	require.NoError(t, err)

	comments, _, err := client.PullRequests.ListPullReviewComments(t.Context(), login.User, repoName, pr.Index, review.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1)

	pullsCmd := &cli.Command{
		Name:     "pulls",
		Commands: []*cli.Command{&pulls.CmdPullsReply},
	}

	err = pullsCmd.Run(context.Background(), []string{
		"pulls",
		"reply",
		strconv.FormatInt(pr.Index, 10),
		strconv.FormatInt(comments[0].ID, 10),
		replyBody,
		"--login",
		login.Name,
		"--repo",
		repo.FullName,
	})
	if err != nil && strings.Contains(err.Error(), "unknown API error: 405") {
		t.Skip("pull review comment replies are not supported by this integration Gitea instance")
	}
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		reviewComments, _, listErr := client.PullRequests.ListPullReviewComments(t.Context(), login.User, repoName, pr.Index, review.ID)
		if listErr != nil {
			t.Logf("failed to list review comments: %v", listErr)
			return false
		}
		for _, reviewComment := range reviewComments {
			if reviewComment.Body == replyBody && reviewComment.ReviewID == review.ID {
				return true
			}
		}
		return false
	}, 10*time.Second, 500*time.Millisecond)
}
