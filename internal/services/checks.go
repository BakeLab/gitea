// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/url"
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

// Tally counts checks by outcome.
func Tally(items []Check) (failing, pending, passed int) {
	for _, item := range items {
		switch {
		case item.Pending():
			pending++
		case item.Passed():
			passed++
		default:
			failing++
		}
	}
	return failing, pending, passed
}

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

func checkFrom(sha, instance string, status *gitea.Status) Check {
	return Check{
		SHA: sha, ID: status.ID, Name: status.Context, State: string(status.State), Description: status.Description,
		URL: absoluteURL(instance, status.TargetURL), CreatedAt: timestamp(status.Created), UpdatedAt: timestamp(status.Updated),
	}
}

// absoluteURL resolves a link that Gitea gives relative to its root, such as the job links of
// Actions statuses, against the instance URL.
func absoluteURL(instance, link string) string {
	base, err := url.Parse(instance)
	if err != nil || link == "" {
		return link
	}
	ref, err := url.Parse(link)
	if err != nil {
		return link
	}
	return base.ResolveReference(ref).String()
}

// CommitChecks returns the latest status of each check context of a commit.
func (s Pulls) CommitChecks(ctx context.Context, sha string) ([]Check, error) {
	return s.checksForCommit(ctx, sha)
}

func (s Pulls) checksForCommit(ctx context.Context, sha string) ([]Check, error) {
	owner, name, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	statuses, err := callAll(ctx, s.Client, 100, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Status, *gitea.Response, error) {
		return c.Repositories.ListStatusesBySHA(ctx, owner, name, sha, gitea.ListStatusesOption{ListOptions: list})
	})
	if err != nil {
		return nil, err
	}
	latest := map[string]Check{}
	for _, status := range statuses {
		current, exists := latest[status.Context]
		if !exists || status.ID > current.ID {
			latest[status.Context] = checkFrom(sha, s.Client.BaseURL, status)
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
	reported := false
	var failures outage
	for {
		sha, err := s.checkHead(ctx, n)
		var items []Check
		if err == nil {
			items, err = s.checksForCommit(ctx, sha)
		}
		if failures.retry(ctx, err) {
			if err := sleepCtx(ctx, interval); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		// The first poll is reported even when empty, so a watcher can say it is waiting.
		if !reported || !slices.Equal(items, previous) {
			reported = true
			if changed != nil {
				if err := changed(items); err != nil {
					return nil, err
				}
			}
			previous = items
		}
		failing, pending, _ := Tally(items)
		if failing > 0 || (pending == 0 && len(items) > 0) {
			current, err := s.checkHead(ctx, n)
			if failures.retry(ctx, err) {
				if err := sleepCtx(ctx, interval); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			if current == sha {
				return items, nil
			}
		}
		failures.passed()
		if err := sleepCtx(ctx, interval); err != nil {
			return nil, err
		}
	}
}
