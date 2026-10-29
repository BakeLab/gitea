// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Issue is the stable command representation of an issue.
type Issue struct {
	Author    string          `json:"author"`
	Labels    []Label         `json:"labels"`
	Ref       string          `json:"ref"`
	ID        int64           `json:"id"`
	Number    int64           `json:"number"`
	Kind      string          `json:"kind"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	URL       string          `json:"url"`
	State     string          `json:"state"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Deadline  *string         `json:"due_date"`
	Assignees []string        `json:"assignees"`
	Milestone *IssueMilestone `json:"milestone"`
	Comments  int64           `json:"comments"`
}

// Kinds of Issue: Gitea lists pull requests among issues.
const (
	KindIssue = "issue"
	KindPull  = "pull"
)

// discussionFrom converts an issue or a pull request listed among issues; the state of a merged
// pull request is "merged" rather than "closed".
func discussionFrom(issue *gitea.Issue) Issue {
	item := issueFrom(issue)
	item.Kind, item.State = KindIssue, discussionState(issue)
	if issue.PullRequest != nil {
		item.Kind = KindPull
	}
	return item
}

// discussionState is the state of an issue, or "merged" for a merged pull request.
func discussionState(issue *gitea.Issue) string {
	if issue.PullRequest != nil && issue.PullRequest.HasMerged {
		return "merged"
	}
	return string(issue.State)
}

// IssueMilestone identifies the milestone associated with an issue or pull request.
type IssueMilestone struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// IssueOptions supplies issue creation input.
type IssueOptions struct {
	Ref       string     `json:"ref,omitempty"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Assignees []string   `json:"assignees,omitempty"`
	Labels    []int64    `json:"labels,omitempty"`
	Milestone int64      `json:"milestone,omitempty"`
	Deadline  *time.Time `json:"due_date,omitempty"`
}

// ListOptions supplies explicit issue pagination and filters.
type ListOptions struct {
	State                              string
	Limit, Page                        int
	Search                             string
	Assignee, Labels, Milestones, Kind string
	Author, Mentions                   string
	Since, Before                      *time.Time
	// NoAssignee keeps only issues without assignees. Gitea has no such filter, so ListPage
	// applies it to every issue it reads.
	NoAssignee bool
}

// Issues implements repository issue operations.
type Issues struct {
	Client     *transport.Client
	Repository string
	// Pulls is set when the numbers name pull requests, which Gitea serves through the issue
	// endpoints also when issues are turned off: failures then name the pull request.
	Pulls bool
}

func (s Issues) endpoint() string { return "repos/" + s.Repository + "/issues" }

// List returns a page of issues, even when the server caps its page size below the limit,
// and reports whether more issues follow.
func (s Issues) List(ctx context.Context, opts ListOptions) ([]Issue, bool, error) {
	page, err := s.ListPage(ctx, opts)
	return page.Items, page.More, err
}

// ListPage is List with the number of issues that match, as the server counts them.
func (s Issues) ListPage(ctx context.Context, opts ListOptions) (Page[Issue], error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[Issue]{}, err
	}
	kind := "issues"
	if opts.Kind != "" {
		kind = opts.Kind
	}
	query := gitea.ListIssueOption{
		State: gitea.StateType(opts.State), Type: gitea.IssueType(kind), KeyWord: opts.Search,
		Labels: filterValues(opts.Labels), Milestones: filterValues(opts.Milestones),
		CreatedBy: opts.Author, AssignedBy: opts.Assignee, MentionedBy: opts.Mentions,
	}
	if opts.Since != nil {
		query.Since = *opts.Since
	}
	if opts.Before != nil {
		query.Before = *opts.Before
	}
	fetch := func(limit, page int) (Page[Issue], error) {
		issues, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			query.ListOptions = list
			return c.Issues.ListRepoIssues(ctx, owner, repo, query)
		})
		if err != nil {
			return Page[Issue]{}, s.listError(ctx, err, kind)
		}
		return convertPage(issues, discussionFrom), nil
	}
	if opts.NoAssignee {
		return filteredPage(opts.Limit, opts.Page, fetch, func(item Issue) bool { return len(item.Assignees) == 0 })
	}
	return fetch(opts.Limit, opts.Page)
}

// filterValues splits a comma-separated filter, which the SDK joins again.
func filterValues(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

// View retrieves one issue.
func (s Issues) View(ctx context.Context, number int64) (Issue, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, discussionFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.GetIssue(ctx, owner, repo, number)
	})
	return item, s.issueError(ctx, err, number)
}

// Kind returns whether number is an issue or a pull request, KindIssue or KindPull, in one
// request. It is "" for a number Gitea does not show, such as an issue of a repository that uses
// an external tracker.
func (s Issues) Kind(ctx context.Context, number int64) (string, error) {
	item, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.GetIssue(ctx, owner, repo, number)
	})
	switch {
	case IsNotFound(err):
		return "", nil
	case err != nil:
		return "", err
	case item.PullRequest != nil:
		return KindPull, nil
	}
	return KindIssue, nil
}

// Find is Kind, but a number Gitea does not show fails with a *NotFoundError, of the repository
// when that is missing, unless the repository keeps its issues in an external tracker.
func (s Issues) Find(ctx context.Context, number int64) (string, error) {
	kind, err := s.Kind(ctx, number)
	if err != nil || kind != "" {
		return kind, err
	}
	repository, lookupErr := (Repositories{Client: s.Client}).get(ctx, s.Repository)
	switch {
	case IsNotFound(lookupErr):
		return "", lookupErr
	case lookupErr == nil && repository.ExternalTracker != nil:
		return "", nil
	}
	return "", &NotFoundError{Kind: "issue or pull request", Key: "#" + strconv.FormatInt(number, 10), Scope: s.Repository}
}

// issueError names issue number in the failure of a request about it, or its repository when
// that is what is missing. Some endpoints, such as the timeline, answer a missing issue with
// HTTP 500 "issue does not exist".
func (s Issues) issueError(ctx context.Context, err error, number int64) error {
	kind, unit := "issue", UnitIssues
	if s.Pulls {
		kind, unit = "pull request", UnitPulls
	}
	key := "#" + strconv.FormatInt(number, 10)
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && strings.Contains(strings.ToLower(apiErr.Message), "issue does not exist") {
		return &NotFoundError{Kind: kind, Key: key, Scope: s.Repository, Err: err}
	}
	if unitErr := unitError(ctx, s.Client, s.Repository, err, unit); unitErr != nil {
		return unitErr
	}
	return Classify(err, kind, key, s.Repository)
}

// listError explains the failure to list discussions of kind "issues", "pulls" or "all".
func (s Issues) listError(ctx context.Context, err error, kind string) error {
	unit := map[string]string{"pulls": UnitPulls, "all": unitDiscussions}[kind]
	if unitErr := unitError(ctx, s.Client, s.Repository, err, cmp.Or(unit, UnitIssues)); unitErr != nil {
		return unitErr
	}
	return Classify(err, "repository", s.Repository, "")
}

// Repository units that Gitea answers with a bare 404 when they are turned off.
const (
	UnitIssues      = "issues"
	UnitPulls       = "pull requests"
	unitDiscussions = "issues and pull requests"
)

// UnitError reports a request to issues or pull requests of a repository that has them turned
// off, or that keeps its issues in an external tracker.
type UnitError struct {
	Repository string
	// Unit is UnitIssues, UnitPulls, or both.
	Unit string
	// Tracker is the URL of the external issue tracker, if the repository uses one.
	Tracker string
	// Instance is the URL of the repository's instance, when known.
	Instance string
	// NamesInstance makes the hint name Instance, which commands do not find on their own.
	NamesInstance bool
}

func (e *UnitError) Error() string {
	if e.Tracker != "" {
		return e.Repository + " uses an external issue tracker: " + e.Tracker
	}
	flags := map[string]string{UnitIssues: "--enable-issues", UnitPulls: "--enable-pull-requests", unitDiscussions: "--enable-issues --enable-pull-requests"}
	repo := e.Repository
	if e.NamesInstance && e.Instance != "" {
		repo = strings.TrimSuffix(e.Instance, "/") + "/" + e.Repository
	}
	return fmt.Sprintf("%s are disabled in %s; turn them on with: gt repo edit %s -R %s", e.Unit, e.Repository, flags[e.Unit], repo)
}

// unitError explains a 404 from the issues or pull requests of repository: Gitea answers the
// same bare 404 for a missing repository, a unit that is turned off, and issues kept in an
// external tracker. It returns nil for any other error, also when the repository lookup fails.
func unitError(ctx context.Context, client *transport.Client, repository string, err error, unit string) error {
	if !IsNotFound(err) {
		return nil
	}
	return unitOff(ctx, client, repository, err, unit)
}

// unitOff returns the UnitError of unit when repository has it turned off, or keeps issues in an
// external tracker, the not-found error of a missing repository, and nil otherwise. err is the
// failure of the request that the unit refused.
func unitOff(ctx context.Context, client *transport.Client, repository string, err error, unit string) error {
	item, lookupErr := (Repositories{Client: client}).get(ctx, repository)
	return unitOffIn(item, lookupErr, repository, err, unit)
}

// unitOffIn is unitOff with the repository already looked up.
func unitOffIn(item *gitea.Repository, lookupErr error, repository string, err error, unit string) error {
	var instance string
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok {
		instance = apiErr.Instance
	}
	switch {
	case IsNotFound(lookupErr):
		return lookupErr
	case lookupErr != nil:
		return nil
	case unit == unitDiscussions && !item.HasIssues && !item.HasPullRequests,
		unit == UnitIssues && !item.HasIssues, unit == UnitPulls && !item.HasPullRequests:
		return &UnitError{Repository: repository, Unit: unit, Instance: instance}
	case unit == UnitIssues && item.ExternalTracker != nil:
		return &UnitError{Repository: repository, Unit: unit, Tracker: item.ExternalTracker.ExternalTrackerURL, Instance: instance}
	}
	return nil
}

// repositoryItemError names kind key in the failure of a request about it, or repository when
// that is what is missing: Gitea answers both with the same 404.
func repositoryItemError(ctx context.Context, client *transport.Client, repository string, err error, kind, key string) error {
	if IsNotFound(err) {
		if repoErr := missingRepository(ctx, client, repository); repoErr != nil {
			return repoErr
		}
	}
	return Classify(err, kind, key, repository)
}

// missingRepository returns the not-found error of repository when it is missing or invisible,
// and nil otherwise, also when the lookup fails another way.
func missingRepository(ctx context.Context, client *transport.Client, repository string) error {
	if _, err := (Repositories{Client: client}).get(ctx, repository); IsNotFound(err) {
		return err
	}
	return nil
}

// Create opens an issue.
func (s Issues) Create(ctx context.Context, options IssueOptions) (Issue, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, discussionFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.CreateIssue(ctx, owner, repo, gitea.CreateIssueOption{
			Title: options.Title, Body: options.Body, Ref: options.Ref, Assignees: options.Assignees,
			Deadline: options.Deadline, Milestone: options.Milestone, Labels: options.Labels,
		})
	})
	// Gitea refuses an issue in a repository without issues with a 403 for anyone but admins.
	if IsForbidden(err) {
		if unitErr := unitOff(ctx, s.Client, s.Repository, err, UnitIssues); unitErr != nil {
			return item, unitErr
		}
	}
	return item, s.listError(ctx, err, "issues")
}

// SetState updates only the selected issue state.
func (s Issues) SetState(ctx context.Context, number int64, state string) (Issue, error) {
	value := gitea.StateType(state)
	return s.edit(ctx, number, gitea.EditIssueOption{State: &value})
}

func (s Issues) edit(ctx context.Context, number int64, opts gitea.EditIssueOption) (Issue, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, discussionFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.EditIssue(ctx, owner, repo, number, opts)
	})
	return item, s.issueError(ctx, err, number)
}
