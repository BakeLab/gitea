// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
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
		var issues []*gitea.Issue
		issues, failures[i] = callAll(ctx, s.Client, 50, lists[i].fetch)
		*lists[i].dest = convertAll(issues, issueReferenceFrom)
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
// where the instance allows that, or with block false lifts that dependency. Like other additions
// and removals, adding a dependency that exists or removing one that does not changes nothing.
func (s Issues) SetBlockedBy(ctx context.Context, n int64, blocker IssueReference, block bool) error {
	return s.changeDependency(ctx, n, blocker, func(ctx context.Context, c *gitea.Client, owner, repo string, meta gitea.IssueMeta) (*gitea.Response, error) {
		if block {
			_, resp, err := c.Issues.CreateIssueDependency(ctx, owner, repo, n, meta)
			return resp, err
		}
		_, resp, err := c.Issues.RemoveIssueDependency(ctx, owner, repo, n, meta)
		return resp, err
	})
}

// SetBlocks makes issue n block issue blocked, which may belong to another repository where the
// instance allows that, or with block false lifts that. It is SetBlockedBy the other way round.
func (s Issues) SetBlocks(ctx context.Context, n int64, blocked IssueReference, block bool) error {
	return s.changeDependency(ctx, n, blocked, func(ctx context.Context, c *gitea.Client, owner, repo string, meta gitea.IssueMeta) (*gitea.Response, error) {
		if block {
			_, resp, err := c.Issues.CreateIssueBlocking(ctx, owner, repo, n, meta)
			return resp, err
		}
		_, resp, err := c.Issues.RemoveIssueBlocking(ctx, owner, repo, n, meta)
		return resp, err
	})
}

// changeDependency runs change, a request about issue n of s and the other issue, and words its
// failure.
func (s Issues) changeDependency(ctx context.Context, n int64, other IssueReference, change func(ctx context.Context, c *gitea.Client, owner, repo string, meta gitea.IssueMeta) (*gitea.Response, error)) error {
	owner, repo, err := splitRepository(other.Repository)
	if err != nil {
		return err
	}
	err = repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, repoOwner, repoName string) (*gitea.Response, error) {
		return change(ctx, c, repoOwner, repoName, gitea.IssueMeta{Index: other.Number, Owner: owner, Name: repo})
	})
	name := other.Repository + "#" + strconv.FormatInt(other.Number, 10)
	kind := "issue"
	if s.Pulls {
		kind = "pull request"
	}
	// Gitea answers 500 when the dependency to add exists or the one to remove does not.
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		message := strings.ToLower(apiErr.Message)
		if strings.Contains(message, "issue dependency does already exist") || strings.Contains(message, "issue dependency does not exist") {
			return nil
		}
		// A 404 names the missing issue by its index, or the missing repository of the other issue.
		if apiErr.Status == http.StatusNotFound {
			missing := &NotFoundError{Kind: kind, Key: "#" + strconv.FormatInt(n, 10), Scope: s.Repository, Err: err}
			switch {
			case strings.Contains(message, "repository does not exist"):
				return &NotFoundError{Kind: "repository", Key: other.Repository, Err: err}
			case !strings.Contains(message, "index: "):
				return s.dependencyRefusal(ctx, n, missing)
			case !strings.Contains(message, fmt.Sprintf("index: %d]", other.Number)):
				return missing
			case other.Number == n && !strings.EqualFold(other.Repository, s.Repository):
				// The index fits both issues; the edited one tells which is missing.
				if _, err := s.View(ctx, n); err != nil {
					return missing
				}
			case other.Number == n:
				return missing
			}
			return &NotFoundError{Kind: "issue", Key: name, Err: err}
		}
	}
	return Classify(err, "issue", name, "")
}

// dependencyRefusal explains the bare 404 that Gitea answers for a dependency change on issue n,
// which also stands for dependencies being disabled or for missing write access to its issues.
func (s Issues) dependencyRefusal(ctx context.Context, n int64, missing error) error {
	if _, err := s.View(ctx, n); err != nil {
		return missing
	}
	if repo, err := (Repositories{Client: s.Client}).get(ctx, s.Repository); err == nil && repo.InternalTracker != nil && !repo.InternalTracker.EnableIssueDependencies {
		return &RefusedError{Text: "issue dependencies are disabled in " + s.Repository}
	}
	return &RefusedError{Text: fmt.Sprintf("changing the dependencies of issue #%d requires write access to the issues of %s", n, s.Repository)}
}
