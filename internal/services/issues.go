// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
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
}

// Issues implements repository issue operations.
type Issues struct {
	Client     *transport.Client
	Repository string
}

func (s Issues) endpoint() string { return "repos/" + s.Repository + "/issues" }

// List returns a page of issues, even when the server caps its page size below the limit,
// and reports whether more issues follow.
func (s Issues) List(ctx context.Context, opts ListOptions) ([]Issue, bool, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return nil, false, err
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
	issues, more, err := listWindow(opts.Limit, opts.Page, func(limit, page int) ([]*gitea.Issue, http.Header, error) {
		query.ListOptions = listOptions(limit, page)
		issues, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Issues.ListRepoIssues(ctx, owner, repo, query)
		})
		return issues, headerOf(resp), err
	})
	if err != nil {
		return nil, false, err
	}
	return convertAll(issues, issueFrom), more, nil
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
	return repoConvert(ctx, s.Client, s.Repository, issueFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.GetIssue(ctx, owner, repo, number)
	})
}

// Create opens an issue.
func (s Issues) Create(ctx context.Context, options IssueOptions) (Issue, error) {
	return repoConvert(ctx, s.Client, s.Repository, issueFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.CreateIssue(ctx, owner, repo, gitea.CreateIssueOption{
			Title: options.Title, Body: options.Body, Ref: options.Ref, Assignees: options.Assignees,
			Deadline: options.Deadline, Milestone: options.Milestone, Labels: options.Labels,
		})
	})
}

// SetState updates only the selected issue state.
func (s Issues) SetState(ctx context.Context, number int64, state string) (Issue, error) {
	value := gitea.StateType(state)
	return s.edit(ctx, number, gitea.EditIssueOption{State: &value})
}

func (s Issues) edit(ctx context.Context, number int64, opts gitea.EditIssueOption) (Issue, error) {
	return repoConvert(ctx, s.Client, s.Repository, issueFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Issue, *gitea.Response, error) {
		return c.Issues.EditIssue(ctx, owner, repo, number, opts)
	})
}
