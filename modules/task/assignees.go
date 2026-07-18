// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"
	"strings"

	gitea "gitea.dev/sdk"
)

// ResolveAssigneeOpts resolves assignee names to IssueAssigneesOption. Returns nil if names is empty.
func ResolveAssigneeOpts(names []string) *gitea.IssueAssigneesOption {
	names = cleanAssignees(names)
	if len(names) == 0 {
		return nil
	}

	return &gitea.IssueAssigneesOption{Assignees: names}
}

// ApplyAssigneeChanges adds and removes assignees on an issue or pull request.
func ApplyAssigneeChanges(requestCtx stdctx.Context, client *gitea.Client, owner, repo string, index int64, add, rm *gitea.IssueAssigneesOption) error {
	if rm != nil {
		_, _, err := client.Issues.DeleteIssueAssignees(requestCtx, owner, repo, index, *rm)
		if err != nil {
			return fmt.Errorf("could not remove assignees: %s", err)
		}
	}
	if add != nil {
		_, _, err := client.Issues.AddIssueAssignees(requestCtx, owner, repo, index, *add)
		if err != nil {
			return fmt.Errorf("could not add assignees: %s", err)
		}
	}
	return nil
}

func cleanAssignees(list []string) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if strings.TrimSpace(a) != "" {
			out = append(out, a)
		}
	}
	return out
}
