// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/print"
)

// CreateIssue creates an issue in the given repo and prints the result
func CreateIssue(requestCtx stdctx.Context, rlogin *config.Login, repoOwner, repoName string, opts gitea.CreateIssueOption) error {
	// title is required
	if len(opts.Title) == 0 {
		return fmt.Errorf("title is required")
	}

	issue, _, err := rlogin.Client().Issues.CreateIssue(requestCtx, repoOwner, repoName, opts)
	if err != nil {
		return fmt.Errorf("could not create issue: %s", err)
	}

	print.IssueDetails(issue, nil)

	fmt.Println(issue.HTMLURL)

	return nil
}
