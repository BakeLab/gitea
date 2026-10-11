// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	gitea "gitea.dev/sdk"
)

// Check represents the latest status of one commit context.
type Check struct {
	SHA         string `json:"sha"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Description string `json:"description"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// Pending reports whether the check has not finished yet.
func (c Check) Pending() bool { return c.State == "pending" }

// Passed reports whether the check finished without blocking a merge; Gitea counts skipped as success.
func (c Check) Passed() bool { return c.State == "success" || c.State == "skipped" }

// Checks returns latest contexts for the PR head in its base repository, as Gitea does.
func (s Pulls) Checks(ctx context.Context, n int64) ([]Check, error) {
	sha, err := s.checkHead(ctx, n)
	if err != nil {
		return nil, err
	}
	return s.checksForCommit(ctx, sha)
}

func (s Pulls) checkHead(ctx context.Context, n int64) (string, error) {
	pull, err := s.get(ctx, n)
	if err != nil {
		return "", err
	}
	if pull.Head == nil || pull.Head.Sha == "" {
		return "", errors.New("Gitea did not return the pull request head commit")
	}
	return pull.Head.Sha, nil
}

func checkFrom(sha string, status *gitea.Status) Check {
	return Check{
		SHA: sha, ID: status.ID, Name: status.Context, State: string(status.State), Description: status.Description,
		URL: status.TargetURL, CreatedAt: timestamp(status.Created), UpdatedAt: timestamp(status.Updated),
	}
}

func (s Pulls) checksForCommit(ctx context.Context, sha string) ([]Check, error) {
	statuses, err := listAll(func(page int) ([]*gitea.Status, error) {
		return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Status, *gitea.Response, error) {
			return c.Repositories.ListStatusesBySHA(ctx, owner, name, sha, gitea.ListStatusesOption{ListOptions: listOptions(100, page)})
		})
	})
	if err != nil {
		return nil, err
	}
	latest := map[string]Check{}
	for _, status := range statuses {
		current, exists := latest[status.Context]
		if !exists || status.ID > current.ID {
			latest[status.Context] = checkFrom(sha, status)
		}
	}
	result := make([]Check, 0, len(latest))
	for _, check := range latest {
		result = append(result, check)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// WatchChecks follows the current PR head until contexts finish or a check fails.
// An empty status set waits for checks to be registered instead of claiming success.
func (s Pulls) WatchChecks(ctx context.Context, n int64, interval time.Duration, changed func([]Check) error) ([]Check, error) {
	if interval <= 0 {
		return nil, errors.New("check polling interval must be positive")
	}
	var previous []Check
	for {
		sha, err := s.checkHead(ctx, n)
		if err != nil {
			return nil, err
		}
		items, err := s.checksForCommit(ctx, sha)
		if err != nil {
			return nil, err
		}
		if !slices.Equal(items, previous) {
			if changed != nil {
				if err := changed(items); err != nil {
					return nil, err
				}
			}
			previous = items
		}
		pending := len(items) == 0
		failed := false
		for _, item := range items {
			if item.Pending() {
				pending = true
			} else if !item.Passed() {
				failed = true
			}
		}
		if failed || !pending {
			current, err := s.checkHead(ctx, n)
			if err != nil {
				return nil, err
			}
			if current == sha {
				return items, nil
			}
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return nil, err
		}
	}
}
