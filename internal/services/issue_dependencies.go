// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// IssueReference identifies an issue that may live in another repository.
// Number is zero for issues the account cannot read.
type IssueReference struct {
	Repository string
	Number     int64
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
	for _, list := range []struct {
		fetch func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error)
		dest  *[]IssueReference
	}{
		{func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Issues.ListIssueDependencies(ctx, owner, repo, number, gitea.ListIssueDependenciesOptions{ListOptions: opts})
		}, &result.BlockedBy},
		{func(ctx context.Context, c *gitea.Client, opts gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Issues.ListIssueBlocks(ctx, owner, repo, number, gitea.ListIssueBlocksOptions{ListOptions: opts})
		}, &result.Blocks},
	} {
		items, err := listAll(func(page int) ([]IssueReference, error) {
			issues, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Issue, *gitea.Response, error) {
				return list.fetch(ctx, c, listOptions(50, page))
			})
			return convertAll(issues, issueReferenceFrom), err
		})
		if api, ok := errors.AsType[*transport.APIError](err); ok && api.Status == http.StatusNotFound {
			return IssueDependencies{}, nil
		}
		if err != nil {
			return IssueDependencies{}, err
		}
		*list.dest = items
	}
	return result, nil
}
