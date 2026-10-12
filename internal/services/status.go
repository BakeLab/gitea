// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// PullStatus groups open pull requests by the selected account's participation. CurrentBranch is
// the pull request of the checked-out branch, which commands fill when they know it.
type PullStatus struct {
	CurrentBranch   *Pull        `json:"current_branch"`
	Authored        []SearchItem `json:"authored"`
	Assigned        []SearchItem `json:"assigned"`
	ReviewRequested []SearchItem `json:"review_requested"`
}

// AccountStatus is an account summary with optional repository Actions results.
type AccountStatus struct {
	Host           string       `json:"host"`
	User           string       `json:"user"`
	Repository     string       `json:"repository"`
	AssignedIssues []SearchItem `json:"assigned_issues"`
	PullRequests   PullStatus   `json:"pull_requests"`
	// Mentions are the open issues and pull requests that mention the account.
	Mentions     []SearchItem `json:"mentions"`
	FailingRuns  []Run        `json:"failing_runs"`
	RunsIncluded bool         `json:"runs_included"`
}

// Status reuses search and Actions workflows.
type Status struct {
	Client     *transport.Client
	Repository string
}

// Pulls collects each category independently; limit applies after repository filtering.
func (s Status) Pulls(ctx context.Context, limit int) (PullStatus, error) {
	result := PullStatus{Authored: []SearchItem{}, Assigned: []SearchItem{}, ReviewRequested: []SearchItem{}}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("status limit must be 1..100")
	}
	scoped := Issues{Client: s.Client, Repository: s.Repository}
	opts := DiscussionSearchOptions{Pulls: true, State: "open", Limit: limit, Page: 1, Authored: true}
	var err error
	result.Authored, err = scoped.Search(ctx, opts)
	if err != nil {
		return result, err
	}
	opts.Authored, opts.Assigned = false, true
	result.Assigned, err = scoped.Search(ctx, opts)
	if err != nil {
		return result, err
	}
	opts.Assigned, opts.ReviewRequested = false, true
	if s.Repository == "" {
		result.ReviewRequested, err = scoped.Search(ctx, opts)
		return result, err
	}
	// Gitea exposes review filters only on instance-wide search. Filter after
	// pagination so requests from other repositories do not consume the limit.
	opts.Owner = strings.SplitN(s.Repository, "/", 2)[0]
	opts.Limit = 100
	for opts.Page = 1; opts.Page <= maxListPages; opts.Page++ {
		items, err := (Issues{Client: s.Client}).Search(ctx, opts)
		if err != nil {
			return result, err
		}
		if len(items) == 0 {
			return result, nil
		}
		for _, item := range items {
			if strings.EqualFold(item.Repository, s.Repository) {
				result.ReviewRequested = append(result.ReviewRequested, item)
				if len(result.ReviewRequested) == limit {
					return result, nil
				}
			}
		}
	}
	return result, fmt.Errorf("review requests exceeded %d pages", maxListPages)
}

// Summary reports open assigned issues and pull requests and mentions. Failing Actions runs of
// runsRepository are included when it is set and has Actions enabled.
func (s Status) Summary(ctx context.Context, limit int, runsRepository string) (AccountStatus, error) {
	result := AccountStatus{Host: s.Client.BaseURL, Repository: runsRepository, AssignedIssues: []SearchItem{}, Mentions: []SearchItem{}, FailingRuns: []Run{}}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("status limit must be 1..100")
	}
	account, err := CurrentAccount(ctx, s.Client)
	if err != nil {
		return result, err
	}
	result.User = account
	result.PullRequests, err = s.Pulls(ctx, limit)
	if err != nil {
		return result, err
	}
	scoped := Issues{Client: s.Client, Repository: s.Repository}
	result.AssignedIssues, err = scoped.Search(ctx, DiscussionSearchOptions{State: "open", Assigned: true, Limit: limit, Page: 1})
	if err != nil {
		return result, err
	}
	for _, pulls := range []bool{false, true} {
		mentions, err := scoped.Search(ctx, DiscussionSearchOptions{State: "open", Mentioned: true, Pulls: pulls, Limit: limit, Page: 1})
		if err != nil {
			return result, err
		}
		result.Mentions = append(result.Mentions, mentions...)
	}
	slices.SortStableFunc(result.Mentions, func(x, y SearchItem) int { return strings.Compare(y.UpdatedAt, x.UpdatedAt) })
	result.Mentions = result.Mentions[:min(len(result.Mentions), limit)]
	if runsRepository != "" {
		repository, err := (Repositories{Client: s.Client}).View(ctx, runsRepository)
		if err != nil {
			return result, err
		}
		if !repository.ActionsEnabled {
			return result, nil
		}
		failing, err := (Actions{Client: s.Client, Repository: runsRepository}).Runs(ctx, RunListOptions{Limit: limit, Page: 1, Statuses: []string{"failure", "timed_out"}})
		if err != nil {
			return result, err
		}
		result.FailingRuns, result.RunsIncluded = failing, true
	}
	return result, nil
}
