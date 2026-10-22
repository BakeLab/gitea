// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// TrackedTime is a stable record of seconds spent on an issue or pull request.
type TrackedTime struct {
	ID          int64  `json:"id"`
	Seconds     int64  `json:"seconds"`
	CreatedAt   string `json:"created_at"`
	User        string `json:"user"`
	Repository  string `json:"repository"`
	IssueNumber int64  `json:"issue_number"`
	IssueTitle  string `json:"issue_title"`
	IssueURL    string `json:"issue_url"`
}

// trackedTimeFrom falls back to repository when the record does not name its issue's repository.
func trackedTimeFrom(record *gitea.TrackedTime, repository string) TrackedTime {
	result := TrackedTime{ID: record.ID, Seconds: record.Time, User: record.UserName, Repository: repository}
	if !record.Created.IsZero() {
		// Gitea returns the creation time of a new record with nanoseconds, but whole seconds in listings.
		result.CreatedAt = record.Created.Format(time.RFC3339)
	}
	if record.Issue != nil {
		result.IssueNumber, result.IssueTitle, result.IssueURL = record.Issue.Index, record.Issue.Title, record.Issue.HTMLURL
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

// ListPage returns a non-nil page of tracked time records with their number, as the server counts them.
func (s Times) ListPage(ctx context.Context, opts TimeOptions) (Page[TrackedTime], error) {
	query := gitea.ListTrackedTimesOptions{User: opts.Username}
	if opts.Since != nil {
		query.Since = *opts.Since
	}
	if opts.Before != nil {
		query.Before = *opts.Before
	}
	records, err := callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.TrackedTime, *gitea.Response, error) {
		query.ListOptions = list
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
	items := convertAll(records.Items, func(record *gitea.TrackedTime) TrackedTime { return trackedTimeFrom(record, s.Repository) })
	switch {
	case err == nil || opts.Mine:
	case opts.Issue != 0:
		err = s.issues().issueError(ctx, err, opts.Issue)
	default:
		err = Classify(err, "repository", s.Repository, "")
	}
	return Page[TrackedTime]{Items: items, More: records.More, Total: records.Total, Counted: records.Counted}, err
}

func (s Times) issues() Issues {
	return Issues{Client: s.Client, Repository: s.Repository}
}

// Find returns the tracked time record id on issue n.
func (s Times) Find(ctx context.Context, n, id int64) (TrackedTime, error) {
	records, err := listEvery(50, func(limit, page int) (Page[TrackedTime], error) {
		return s.ListPage(ctx, TimeOptions{Issue: n, Limit: limit, Page: page})
	})
	if err != nil {
		return TrackedTime{}, err
	}
	for _, record := range records {
		if record.ID == id {
			return record, nil
		}
	}
	return TrackedTime{}, NotFound("tracked time", strconv.FormatInt(id, 10), fmt.Sprintf("%s#%d", s.Repository, n))
}

// Create records a nonzero number of seconds on one issue.
func (s Times) Create(ctx context.Context, n, seconds int64) (TrackedTime, error) {
	record, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.TrackedTime, *gitea.Response, error) {
		return c.Issues.AddTime(ctx, owner, repo, n, gitea.AddTimeOption{Time: seconds})
	})
	if err != nil {
		return TrackedTime{}, s.issues().issueError(ctx, err, n)
	}
	result := trackedTimeFrom(record, s.Repository)
	result.IssueNumber = n
	return result, nil
}

// Delete removes one tracked time record from the selected issue.
func (s Times) Delete(ctx context.Context, n, id int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteTime(ctx, owner, repo, n, id)
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && strings.Contains(apiErr.Message, "tracked_time does not exist") {
		return NotFound("tracked time", strconv.FormatInt(id, 10), fmt.Sprintf("%s#%d", s.Repository, n))
	}
	return s.issues().issueError(ctx, err, n)
}

// Reset removes the tracked time records of the caller on the selected issue, and reports whether
// there were any.
func (s Times) Reset(ctx context.Context, n int64) (bool, error) {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.ResetIssueTime(ctx, owner, repo, n)
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && strings.Contains(apiErr.Message, "tracked_time does not exist") {
		return false, nil
	}
	return err == nil, s.issues().issueError(ctx, err, n)
}
