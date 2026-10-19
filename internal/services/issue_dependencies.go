// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	gitea "gitea.dev/sdk"
)

// IssueReference identifies an issue that may live in another repository.
// Number is zero for issues the account cannot read.
type IssueReference struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
}

// IssueDependencies lists the issues blocking one issue and the issues it blocks.
type IssueDependencies struct {
	BlockedBy, Blocks []IssueReference
}

func issueReferenceFrom(issue *gitea.Issue) IssueReference {
	reference := IssueReference{Number: issue.Index}
	if issue.Repository != nil {
		reference.Repository = issue.Repository.FullName
	}
	return reference
}

// Dependencies reads both dependency directions; servers or repositories without
// dependency support report none.
func (s Issues) Dependencies(ctx context.Context, number int64) (IssueDependencies, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return IssueDependencies{}, err
	}
	result := IssueDependencies{}
	lists := []struct {
		fetch func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error)
		dest  *[]IssueReference
	}{
		{func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Issues.ListIssueDependencies(ctx, owner, repo, number, gitea.ListIssueDependenciesOptions{ListOptions: opts})
		}, &result.BlockedBy},
		{func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Issues.ListIssueBlocks(ctx, owner, repo, number, gitea.ListIssueBlocksOptions{ListOptions: opts})
		}, &result.Blocks},
	}
	failures := make([]error, len(lists))
	_ = concurrently(len(lists), func(i int) error {
		*lists[i].dest, failures[i] = listAll(func(page int) ([]IssueReference, error) {
			issues, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Issue, *gitea.Response, error) {
				return lists[i].fetch(ctx, c, listOptions(50, page))
			})
			return convertAll(issues, issueReferenceFrom), err
		})
		return nil
	})
	for _, err := range failures {
		if err != nil && !IsNotFound(err) {
			return IssueDependencies{}, err
		}
	}
	// Servers or repositories without dependency support answer 404.
	if slices.ContainsFunc(failures, IsNotFound) {
		return IssueDependencies{BlockedBy: []IssueReference{}, Blocks: []IssueReference{}}, nil
	}
	return result, nil
}

// SetBlockedBy makes issue n blocked by issue blocker, which may belong to another repository
// where the instance allows that, or with block false lifts that dependency.
func (s Issues) SetBlockedBy(ctx context.Context, n int64, blocker IssueReference, block bool) error {
	owner, repo, err := splitRepository(blocker.Repository)
	if err != nil {
		return err
	}
	method := http.MethodPost
	if !block {
		method = http.MethodDelete
	}
	// TODO(sdk): IssueMeta lacks the owner and repo that Gitea needs to find the blocking issue.
	body := map[string]any{"owner": owner, "repo": repo, "index": blocker.Number}
	err = sendJSON(ctx, s.Client, method, fmt.Sprintf("%s/%d/dependencies", s.endpoint(), n), body, nil)
	return Classify(err, "issue", blocker.Repository+"#"+strconv.FormatInt(blocker.Number, 10), "")
}
