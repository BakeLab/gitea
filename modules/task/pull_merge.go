// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
)

// PullMerge merges a PR
func PullMerge(requestCtx stdctx.Context, login *config.Login, repoOwner, repoName string, index int64, opt gitea.MergePullRequestOption) error {
	client := login.Client()
	success, _, err := client.PullRequests.MergePullRequest(requestCtx, repoOwner, repoName, index, opt)
	if err != nil {
		return err
	}
	if success {
		return nil
	}
	return fmt.Errorf("failed to merge PR #%d: %s", index,
		mergeFailureReason(requestCtx, client, repoOwner, repoName, index))
}

// mergeFailureReason returns why merging was refused. The SDK reports refusal as
// success=false and discards Gitea's explanatory body, so the reason has to be
// re-derived from the PR. Costs one API call, on the failure path only.
func mergeFailureReason(requestCtx stdctx.Context, client *gitea.Client, repoOwner, repoName string, index int64) string {
	// Fallback naming the conditions tea cannot observe, used when the PR looks
	// mergeable but the merge was refused anyway.
	const refused = "the server refused the merge; check required status checks, requested reviews, or branch protection rules"

	pr, _, err := client.PullRequests.GetPullRequest(requestCtx, repoOwner, repoName, index)
	if err != nil || pr == nil {
		return refused
	}

	switch {
	case pr.HasMerged:
		return "it has already been merged"
	case pr.State == gitea.StateClosed:
		return "it is closed"
	case pr.Draft:
		return "it is a draft; mark it ready for review first"
	case !pr.Mergeable:
		return "it has conflicting files or is otherwise not mergeable"
	default:
		return refused
	}
}
