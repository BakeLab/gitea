// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
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
	Author     string `json:"author"`
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
	page, err := s.SearchPage(ctx, opts)
	return page.Items, err
}

// SearchPage is Search with what the server reports about further results.
func (s Issues) SearchPage(ctx context.Context, opts DiscussionSearchOptions) (Page[SearchItem], error) {
	kind, resultType := gitea.IssueTypeIssue, KindIssue
	if opts.Pulls {
		kind, resultType = gitea.IssueTypePull, KindPull
	}
	if opts.Team != "" && opts.Owner == "" {
		return Page[SearchItem]{}, fmt.Errorf("team search requires an organization owner")
	}
	search := s.searchInstance
	if s.Repository != "" {
		search = s.searchRepository
	}
	issues, err := search(ctx, opts, kind)
	if err != nil {
		return Page[SearchItem]{}, err
	}
	items := convertAll(issues.Items, func(issue *gitea.Issue) SearchItem {
		repository := s.Repository
		if issue.Repository != nil {
			repository = issue.Repository.FullName
		}
		return SearchItem{
			ID: issue.ID, Number: issue.Index, Title: issue.Title, Body: issue.Body, URL: issue.HTMLURL, State: discussionState(issue),
			Type: resultType, Repository: repository, Author: login(issue.Poster), CreatedAt: timestamp(issue.Created), UpdatedAt: timestamp(issue.Updated),
		}
	})
	return Page[SearchItem]{Items: items, More: issues.More, Total: issues.Total, Counted: issues.Counted}, nil
}

func (s Issues) searchRepository(ctx context.Context, opts DiscussionSearchOptions, kind gitea.IssueType) (Page[*gitea.Issue], error) {
	if opts.ReviewRequested || opts.Reviewed || opts.Team != "" {
		return Page[*gitea.Issue]{}, &CapabilityError{Feature: "repository-scoped review or team search", Reason: "Gitea supports review and team filters on instance-wide search only"}
	}
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[*gitea.Issue]{}, err
	}
	query := gitea.ListIssueOption{
		State: gitea.StateType(opts.State), Type: kind, KeyWord: opts.Query,
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
			return Page[*gitea.Issue]{}, err
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
	issues, err := callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
		query.ListOptions = list
		return c.Issues.ListRepoIssues(ctx, owner, repo, query)
	})
	if err != nil {
		return issues, s.listError(ctx, err, string(kind))
	}
	return issues, nil
}

func (s Issues) searchInstance(ctx context.Context, opts DiscussionSearchOptions, kind gitea.IssueType) (Page[*gitea.Issue], error) {
	query := gitea.ListIssueOption{
		State: gitea.StateType(opts.State), Type: kind, KeyWord: opts.Query, CreatedBy: opts.Author, Owner: opts.Owner, Team: opts.Team,
		Since: deref(opts.Since), Before: deref(opts.Before),
		Assigned: opts.Assigned, Created: opts.Authored, Mentioned: opts.Mentioned, ReviewRequested: opts.ReviewRequested, Reviewed: opts.Reviewed,
	}
	if opts.Labels != "" {
		query.Labels = []string{opts.Labels}
	}
	if opts.Milestones != "" {
		query.Milestones = []string{opts.Milestones}
	}
	page, err := callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
		query.ListOptions = list
		return c.Issues.ListIssues(ctx, query)
	})
	return page, Classify(err, "", "", "")
}
