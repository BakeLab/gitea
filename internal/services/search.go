// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	gitea "gitea.dev/sdk"
)

// SearchItem is shared issue/PR search metadata; ID is Gitea's issue-tracker
// record ID, including for PRs. Repository and Number identify the resource
// across search and pull endpoints, which use different internal IDs.
type SearchItem struct {
	ID         int64  `json:"id"`
	Number     int64  `json:"number"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Type       string `json:"type"`
	Repository string `json:"repository"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// DiscussionSearchOptions specifies issue or pull request filters.
type DiscussionSearchOptions struct {
	Query, State, Owner, Team, Author, Labels, Milestones           string
	Since, Before                                                   *time.Time
	Pulls, Assigned, Authored, Mentioned, ReviewRequested, Reviewed bool
	Limit, Page                                                     int
}

// Search searches one repository or accessible repositories across the selected instance.
func (s Issues) Search(ctx context.Context, opts DiscussionSearchOptions) ([]SearchItem, error) {
	kind, resultType := gitea.IssueTypeIssue, "issue"
	if opts.Pulls {
		kind, resultType = gitea.IssueTypePull, "pr"
	}
	if opts.Team != "" && opts.Owner == "" {
		return nil, fmt.Errorf("team search requires an organization owner")
	}
	search := s.searchInstance
	if s.Repository != "" {
		search = s.searchRepository
	}
	issues, err := search(ctx, opts, kind)
	if err != nil {
		return nil, err
	}
	return convertAll(issues, func(issue *gitea.Issue) SearchItem {
		repository := s.Repository
		if issue.Repository != nil {
			repository = issue.Repository.FullName
		}
		return SearchItem{
			ID: issue.ID, Number: issue.Index, Title: issue.Title, Body: issue.Body, URL: issue.HTMLURL, State: string(issue.State),
			Type: resultType, Repository: repository, CreatedAt: timestamp(issue.Created), UpdatedAt: timestamp(issue.Updated),
		}
	}), nil
}

func (s Issues) searchRepository(ctx context.Context, opts DiscussionSearchOptions, kind gitea.IssueType) ([]*gitea.Issue, error) {
	if opts.ReviewRequested || opts.Reviewed {
		return nil, &CapabilityError{Feature: "repository-scoped review search", Reason: "Gitea supports review filters on instance-wide search only; omit --repo or GITEA_CLI_REPO"}
	}
	if opts.Team != "" {
		return nil, &CapabilityError{Feature: "repository-scoped team search", Reason: "Gitea supports team filters on instance-wide search only; omit --repo or GITEA_CLI_REPO"}
	}
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	query := gitea.ListIssueOption{
		ListOptions: listOptions(opts.Limit, opts.Page), State: gitea.StateType(opts.State), Type: kind, KeyWord: opts.Query,
		Labels: filterValues(opts.Labels), Milestones: filterValues(opts.Milestones), CreatedBy: opts.Author, Owner: opts.Owner,
	}
	if opts.Since != nil {
		query.Since = *opts.Since
	}
	if opts.Before != nil {
		query.Before = *opts.Before
	}
	if opts.Authored || opts.Assigned || opts.Mentioned {
		account, err := CurrentAccount(ctx, s.Client)
		if err != nil {
			return nil, err
		}
		if opts.Authored {
			query.CreatedBy = account
		}
		if opts.Assigned {
			query.AssignedBy = account
		}
		if opts.Mentioned {
			query.MentionedBy = account
		}
	}
	issues, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Issue, *gitea.Response, error) {
		return c.Issues.ListRepoIssues(ctx, owner, repo, query)
	})
	return issues, err
}

func (s Issues) searchInstance(ctx context.Context, opts DiscussionSearchOptions, kind gitea.IssueType) ([]*gitea.Issue, error) {
	query := url.Values{"q": {opts.Query}, "state": {opts.State}, "type": {string(kind)}, "limit": {strconv.Itoa(opts.Limit)}, "page": {strconv.Itoa(opts.Page)}}
	for key, value := range map[string]string{"owner": opts.Owner, "team": opts.Team, "created_by": opts.Author, "labels": opts.Labels, "milestones": opts.Milestones} {
		if value != "" {
			query.Set(key, value)
		}
	}
	for key, value := range map[string]*time.Time{"since": opts.Since, "before": opts.Before} {
		if value != nil {
			query.Set(key, value.Format(time.RFC3339))
		}
	}
	for key, enabled := range map[string]bool{"created": opts.Authored, "assigned": opts.Assigned, "mentioned": opts.Mentioned, "review_requested": opts.ReviewRequested, "reviewed": opts.Reviewed} {
		if enabled {
			query.Set(key, "true")
		}
	}
	var issues []*gitea.Issue
	// TODO(sdk): ListIssueOption lacks the assigned, created, mentioned, review_requested and reviewed flags.
	err := getJSON(ctx, s.Client, "repos/issues/search?"+query.Encode(), &issues)
	return issues, err
}
