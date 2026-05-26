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
	if !success {
		return fmt.Errorf("failed to merge PR, is it still open?")
	}
	return nil
}
