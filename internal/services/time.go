// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// TrackedTime is a stable record of seconds spent on an issue or pull request.
type TrackedTime struct {
	ID          int64  `json:"id"`
	Seconds     int64  `json:"seconds"`
	CreatedAt   string `json:"created_at"`
	User        string `json:"user"`
	Repository  string `json:"repository"`
	IssueNumber int64  `json:"issue_number"`
	IssueURL    string `json:"issue_url"`
}

// trackedTimeFrom falls back to repository when the record does not name its issue's repository.
func trackedTimeFrom(record *gitea.TrackedTime, repository string) TrackedTime {
	result := TrackedTime{ID: record.ID, Seconds: record.Time, CreatedAt: timestamp(record.Created), User: record.UserName, Repository: repository}
	if record.Issue != nil {
		result.IssueNumber = record.Issue.Index
		result.IssueURL = record.Issue.HTMLURL
		if record.Issue.Repository != nil {
			result.Repository = record.Issue.Repository.FullName
		}
	}
	return result
}

// TimeOptions supplies an explicit scope, pagination and date filters.
type TimeOptions struct {
	Mine          bool
	Issue         int64
	Username      string
	Limit, Page   int
	Since, Before *time.Time
}

// Times implements repository and account time tracking.
type Times struct {
	Client     *transport.Client
	Repository string
}

// List returns a non-nil page of tracked time records.
func (s Times) List(ctx context.Context, opts TimeOptions) ([]TrackedTime, error) {
	query := gitea.ListTrackedTimesOptions{ListOptions: listOptions(opts.Limit, opts.Page), User: opts.Username}
	if opts.Since != nil {
		query.Since = *opts.Since
	}
	if opts.Before != nil {
		query.Before = *opts.Before
	}
	records, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.TrackedTime, *gitea.Response, error) {
		if opts.Mine {
			return c.Issues.ListMyTrackedTimes(ctx, query)
		}
		owner, repo, err := splitRepository(s.Repository)
		if err != nil {
			return nil, nil, err
		}
		if opts.Issue != 0 {
			return c.Issues.ListIssueTrackedTimes(ctx, owner, repo, opts.Issue, query)
		}
		return c.Issues.ListRepoTrackedTimes(ctx, owner, repo, query)
	})
	if err != nil {
		return nil, err
	}
	return convertAll(records, func(record *gitea.TrackedTime) TrackedTime { return trackedTimeFrom(record, s.Repository) }), nil
}

// Create records a nonzero number of seconds on one issue.
func (s Times) Create(ctx context.Context, n, seconds int64) (TrackedTime, error) {
	record, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.TrackedTime, *gitea.Response, error) {
		return c.Issues.AddTime(ctx, owner, repo, n, gitea.AddTimeOption{Time: seconds})
	})
	if err != nil {
		return TrackedTime{}, err
	}
	result := trackedTimeFrom(record, s.Repository)
	result.IssueNumber = n
	return result, nil
}

// Delete removes one tracked time record from the selected issue.
func (s Times) Delete(ctx context.Context, n, id int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteTime(ctx, owner, repo, n, id)
	})
}

// Reset removes all tracked time records on the selected issue.
func (s Times) Reset(ctx context.Context, n int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.ResetIssueTime(ctx, owner, repo, n)
	})
}
