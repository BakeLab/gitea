// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
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
	Host string `json:"host"`
	User string `json:"user"`
	// Repository is the repository the summary is scoped to, empty for all repositories.
	Repository     string       `json:"repository"`
	AssignedIssues []SearchItem `json:"assigned_issues"`
	PullRequests   PullStatus   `json:"pull_requests"`
	// Mentions are the open issues and pull requests that mention the account.
	Mentions []SearchItem `json:"mentions"`
	// RunsRepository is the repository whose failing runs are listed, when RunsIncluded.
	RunsRepository string `json:"runs_repository"`
	FailingRuns    []Run  `json:"failing_runs"`
	RunsIncluded   bool   `json:"runs_included"`
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
		return result, errors.New("status limit must be 1..100")
	}
	scoped := Issues{Client: s.Client, Repository: s.Repository}
	search := func(dest *[]SearchItem, opts DiscussionSearchOptions) func() error {
		opts.Pulls, opts.State, opts.Limit, opts.Page = true, "open", limit, 1
		return func() (err error) {
			*dest, err = scoped.Search(ctx, opts)
			return err
		}
	}
	loaders := []func() error{
		search(&result.Authored, DiscussionSearchOptions{Authored: true}),
		search(&result.Assigned, DiscussionSearchOptions{Assigned: true}),
	}
	if s.Repository == "" {
		loaders = append(loaders, search(&result.ReviewRequested, DiscussionSearchOptions{ReviewRequested: true}))
	} else {
		// The repository searches share this lookup, which concurrent callers would each send.
		if _, err := CurrentAccount(ctx, s.Client); err != nil {
			return result, err
		}
		loaders = append(loaders, func() (err error) {
			result.ReviewRequested, err = s.requestedReviews(ctx, limit, result.ReviewRequested)
			return err
		})
	}
	err := loadFirstError(loaders...)
	return result, err
}

// requestedReviews appends up to limit open pull requests of the repository that request a review.
// Gitea exposes review filters only on instance-wide search. Filter after pagination so requests
// from other repositories do not consume the limit.
func (s Status) requestedReviews(ctx context.Context, limit int, found []SearchItem) ([]SearchItem, error) {
	opts := DiscussionSearchOptions{
		Pulls: true, State: "open", Limit: 100, ReviewRequested: true,
		Owner: strings.SplitN(s.Repository, "/", 2)[0],
	}
	seen := 0
	for opts.Page = 1; opts.Page <= maxListPages; opts.Page++ {
		page, err := (Issues{Client: s.Client}).SearchPage(ctx, opts)
		if err != nil {
			return found, err
		}
		for _, item := range page.Items {
			if strings.EqualFold(item.Repository, s.Repository) {
				found = append(found, item)
				if len(found) == limit {
					return found, nil
				}
			}
		}
		seen += len(page.Items)
		if len(page.Items) == 0 || page.Counted && !page.More && seen == page.Total {
			return found, nil
		}
	}
	return found, fmt.Errorf("review requests exceeded %d pages", maxListPages)
}

// Summary reports open assigned issues and pull requests and mentions. Failing Actions runs of
// runsRepository are included when it is set and has Actions enabled. A runsRepository that was
// only discovered from a clone is left out when the account cannot read it, instead of failing.
func (s Status) Summary(ctx context.Context, limit int, runsRepository string, discovered bool) (AccountStatus, error) {
	result := AccountStatus{Host: s.Client.BaseURL, Repository: s.Repository, AssignedIssues: []SearchItem{}, Mentions: []SearchItem{}, FailingRuns: []Run{}}
	if limit < 1 || limit > 100 {
		return result, errors.New("status limit must be 1..100")
	}
	account, err := CurrentAccount(ctx, s.Client)
	if err != nil {
		return result, err
	}
	result.User = account
	scoped := Issues{Client: s.Client, Repository: s.Repository}
	var mentionedIssues, mentionedPulls []SearchItem
	err = loadFirstError(
		func() (err error) {
			result.PullRequests, err = s.Pulls(ctx, limit)
			return err
		},
		func() (err error) {
			result.AssignedIssues, err = scoped.Search(ctx, DiscussionSearchOptions{State: "open", Assigned: true, Limit: limit, Page: 1})
			return err
		},
		func() (err error) {
			mentionedIssues, err = scoped.Search(ctx, DiscussionSearchOptions{State: "open", Mentioned: true, Limit: limit, Page: 1})
			return err
		},
		func() (err error) {
			mentionedPulls, err = scoped.Search(ctx, DiscussionSearchOptions{State: "open", Mentioned: true, Pulls: true, Limit: limit, Page: 1})
			return err
		},
	)
	if err != nil {
		return result, err
	}
	result.Mentions = slices.Concat(result.Mentions, mentionedIssues, mentionedPulls)
	slices.SortStableFunc(result.Mentions, func(x, y SearchItem) int { return strings.Compare(y.UpdatedAt, x.UpdatedAt) })
	result.Mentions = result.Mentions[:min(len(result.Mentions), limit)]
	if runsRepository != "" {
		repository, err := (Repositories{Client: s.Client}).View(ctx, runsRepository)
		if err != nil {
			if discovered && (IsNotFound(err) || IsForbidden(err)) {
				return result, nil
			}
			return result, err
		}
		if !repository.ActionsEnabled {
			return result, nil
		}
		actions := Actions{Client: s.Client, Repository: runsRepository}
		failing, err := actions.Runs(ctx, RunListOptions{Limit: limit, Page: 1, Statuses: []string{"failure"}})
		if err != nil {
			if discovered && (IsNotFound(err) || IsForbidden(err)) {
				return result, nil
			}
			return result, err
		}
		_ = actions.nameRuns(ctx, failing) // best effort like NameRun, so a summary never fails on it
		result.RunsRepository, result.FailingRuns, result.RunsIncluded = runsRepository, failing, true
	}
	return result, nil
}
