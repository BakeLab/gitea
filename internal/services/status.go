// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// PullStatus groups open pull requests by the selected account's participation.
type PullStatus struct {
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
	FailingRuns    []Run        `json:"failing_runs"`
	RunsIncluded   bool         `json:"runs_included"`
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

// Summary reports open assigned issues and pull requests. Actions results are
// included only when requested and a repository has been selected.
func (s Status) Summary(ctx context.Context, limit int, runs bool) (AccountStatus, error) {
	result := AccountStatus{Host: s.Client.BaseURL, Repository: s.Repository, AssignedIssues: []SearchItem{}, FailingRuns: []Run{}}
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
	result.AssignedIssues, err = (Issues{Client: s.Client, Repository: s.Repository}).Search(ctx, DiscussionSearchOptions{State: "open", Assigned: true, Limit: limit, Page: 1})
	if err != nil {
		return result, err
	}
	if runs && s.Repository != "" {
		repository, err := (Repositories{Client: s.Client}).View(ctx, s.Repository)
		if err != nil {
			return result, err
		}
		if !repository.ActionsEnabled {
			return result, nil
		}
		failing, err := (Actions{Client: s.Client, Repository: s.Repository}).Runs(ctx, RunListOptions{Limit: limit, Page: 1, Statuses: []string{"failure", "timed_out"}})
		if err != nil {
			return result, err
		}
		result.FailingRuns, result.RunsIncluded = failing, true
	}
	return result, nil
}
