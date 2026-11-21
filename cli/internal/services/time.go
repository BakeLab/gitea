// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	case trackingOff(err):
		err = s.trackingError()
	case opts.Issue != 0:
		err = s.issues().issueError(ctx, err, opts.Issue)
	default:
		err = s.issues().listError(ctx, err, "issues")
	}
	return Page[TrackedTime]{Items: items, More: records.More, Total: records.Total, Counted: records.Counted}, err
}

func (s Times) issues() Issues {
	return Issues{Client: s.Client, Repository: s.Repository}
}

// trackingOff reports whether err is Gitea refusing because time tracking is disabled, which it
// answers with 404 on the records of an issue and with 400 elsewhere.
func trackingOff(err error) bool {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	return ok && (strings.Contains(apiErr.Message, "timetracker is disabled") || strings.Contains(apiErr.Message, "time tracking disabled"))
}

func (s Times) trackingError() error {
	return fmt.Errorf("time tracking is disabled in %s", s.Repository)
}

// issueError explains the failure of a request about the time tracked on issue n.
func (s Times) issueError(ctx context.Context, err error, n int64) error {
	if trackingOff(err) {
		return s.trackingError()
	}
	return s.issues().issueError(ctx, err, n)
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
		return TrackedTime{}, s.issueError(ctx, err, n)
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
	return s.issueError(ctx, err, n)
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
	return err == nil, s.issueError(ctx, err, n)
}

// Stopwatch is a running timer of the account on an issue or pull request.
type Stopwatch struct {
	Repository  string `json:"repository"`
	IssueNumber int64  `json:"issue_number"`
	IssueTitle  string `json:"issue_title"`
	// Seconds is how long it has run, as Gitea reads it when asked.
	Seconds   int64  `json:"seconds"`
	StartedAt string `json:"started_at"`
}

func stopwatchFrom(watch *gitea.StopWatch) Stopwatch {
	return Stopwatch{
		Repository: watch.RepoOwnerName + "/" + watch.RepoName, IssueNumber: watch.IssueIndex, IssueTitle: watch.IssueTitle,
		Seconds: watch.Seconds, StartedAt: timestamp(watch.Created),
	}
}

// Running lists the stopwatches that the account has running, in any repository.
func (s Times) Running(ctx context.Context) ([]Stopwatch, error) {
	watches, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.StopWatch, *gitea.Response, error) {
		return c.Issues.ListMyStopwatches(ctx, gitea.ListStopwatchesOptions{ListOptions: list})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(watches, stopwatchFrom), nil
}

// RunningOn returns the stopwatch that the listing shows on issue n, for its title and elapsed time.
// Only Gitea's answer to a request about the stopwatch tells whether one runs: the listing can fail
// on a token that may change the issue, and knows nothing of a missing or renamed repository.
func (s Times) RunningOn(ctx context.Context, n int64) (Stopwatch, bool) {
	running, err := s.Running(ctx)
	if err != nil {
		return Stopwatch{}, false
	}
	for _, watch := range running {
		if watch.IssueNumber == n && strings.EqualFold(watch.Repository, s.Repository) {
			return watch, true
		}
	}
	return Stopwatch{}, false
}

// ErrNoStopwatch reports that the account has no stopwatch running on an issue.
var ErrNoStopwatch = errors.New("no stopwatch is running")

// stopwatchError words the refusals of Gitea about a stopwatch.
func (s Times) stopwatchError(ctx context.Context, err error, n int64) error {
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		message := strings.ToLower(apiErr.Message)
		switch {
		case apiErr.Status == http.StatusConflict && strings.Contains(message, "again if it already exists"):
			return fmt.Errorf("a stopwatch is already running on %s#%d", s.Repository, n)
		case apiErr.Status == http.StatusConflict && strings.Contains(message, "non-existent stopwatch"):
			return fmt.Errorf("%w on %s#%d", ErrNoStopwatch, s.Repository, n)
		case apiErr.Status == http.StatusForbidden && apiErr.Message == "": // others name the cause, such as a token scope
			return s.stopwatchRefusal(ctx, err, n)
		}
	}
	return s.issueError(ctx, err, n)
}

// stopwatchRefusal explains the bare 403 that Gitea answers for a stopwatch on issue n, which stands
// for time tracking being disabled as well as for missing write access.
func (s Times) stopwatchRefusal(ctx context.Context, err error, n int64) error {
	if repo, lookupErr := (Repositories{Client: s.Client}).get(ctx, s.Repository); lookupErr == nil && repo.InternalTracker != nil && !repo.InternalTracker.EnableTimeTracker {
		return s.trackingError()
	}
	return &RefusedError{Text: fmt.Sprintf("a stopwatch on %s#%d requires write access to the issue or pull request", s.Repository, n), Err: err}
}

// Start starts a stopwatch on issue n.
func (s Times) Start(ctx context.Context, n int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.StartIssueStopWatch(ctx, owner, repo, n)
	})
	return s.stopwatchError(ctx, err, n)
}

// Stop stops the stopwatch on issue n and logs the time it ran as tracked time.
func (s Times) Stop(ctx context.Context, n int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.StopIssueStopWatch(ctx, owner, repo, n)
	})
	return s.stopwatchError(ctx, err, n)
}

// Cancel discards the stopwatch on issue n without logging its time.
func (s Times) Cancel(ctx context.Context, n int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Issues.DeleteIssueStopwatch(ctx, owner, repo, n)
	})
	return s.stopwatchError(ctx, err, n)
}
