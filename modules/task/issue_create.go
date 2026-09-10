// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
)

// CreateIssue creates an issue in the given repo and returns the created issue
func CreateIssue(requestCtx stdctx.Context, rlogin *config.Login, repoOwner, repoName string, opts gitea.CreateIssueOption) (*gitea.Issue, error) {
	// title is required
	if len(opts.Title) == 0 {
		return nil, fmt.Errorf("title is required")
	}

	issue, _, err := rlogin.Client().Issues.CreateIssue(requestCtx, repoOwner, repoName, opts)
	if err != nil {
		return nil, fmt.Errorf("could not create issue: %s", err)
	}

	return issue, nil
}
