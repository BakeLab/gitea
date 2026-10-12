// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gitea "gitea.dev/sdk"
)

// ReviewRequestInput names users and teams within the repository's organization.
type ReviewRequestInput struct {
	Users []string `json:"reviewers,omitempty"`
	Teams []string `json:"team_reviewers,omitempty"`
}

// ReviewRequestChanges adds and removes explicitly named review requests.
type ReviewRequestChanges struct {
	Add, Remove ReviewRequestInput
}

// Validate rejects empty, duplicated, and conflicting names before any request.
func (c ReviewRequestChanges) Validate() error {
	if len(c.Add.Users)+len(c.Add.Teams)+len(c.Remove.Users)+len(c.Remove.Teams) == 0 {
		return errors.New("supply at least one reviewer or team")
	}
	for _, pair := range []struct {
		add, remove []string
		noun        string
	}{{c.Add.Users, c.Remove.Users, "reviewer"}, {c.Add.Teams, c.Remove.Teams, "team"}} {
		seen := map[string]bool{}
		for _, names := range [][]string{pair.add, pair.remove} {
			for _, name := range names {
				if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
					return fmt.Errorf("%s names cannot be empty or have surrounding whitespace", pair.noun)
				}
				key := strings.ToLower(name)
				if seen[key] {
					return fmt.Errorf("%s %q is duplicated or present in both add and remove", pair.noun, name)
				}
				seen[key] = true
			}
		}
	}
	return nil
}

// ChangeRequests returns the PR after changes and reports partial mutation failures.
func (s Reviews) ChangeRequests(ctx context.Context, n int64, changes ReviewRequestChanges) (Pull, error) {
	if err := s.changeRequests(ctx, n, changes); err != nil {
		return Pull{}, err
	}
	item, err := (Pulls{Client: s.Client, Repository: s.Repository}).View(ctx, n)
	if err != nil {
		return Pull{}, fmt.Errorf("review requests were updated, but reading the pull request failed: %w", err)
	}
	return item, nil
}

// changeRequests applies the review request changes to pull request n.
func (s Reviews) changeRequests(ctx context.Context, n int64, changes ReviewRequestChanges) error {
	if err := changes.Validate(); err != nil {
		return err
	}
	if len(changes.Add.Teams)+len(changes.Remove.Teams) > 0 {
		if err := s.CheckRequestTeams(ctx, append(append([]string{}, changes.Add.Teams...), changes.Remove.Teams...)); err != nil {
			return err
		}
	}
	mutated := false
	for _, action := range []struct {
		remove bool
		input  ReviewRequestInput
	}{{true, changes.Remove}, {false, changes.Add}} {
		if len(action.input.Users)+len(action.input.Teams) == 0 {
			continue
		}
		option := gitea.PullReviewRequestOptions{Reviewers: action.input.Users, TeamReviewers: action.input.Teams}
		err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
			if action.remove {
				return c.PullRequests.DeleteReviewRequests(ctx, owner, name, n, option)
			}
			return c.PullRequests.CreateReviewRequests(ctx, owner, name, n, option)
		})
		if err != nil {
			if mutated {
				return fmt.Errorf("reviewer removals succeeded, but adding reviewers failed: %w", err)
			}
			return fmt.Errorf("review request update failed; Gitea may have applied some requests: %w", err)
		}
		mutated = true
	}
	return nil
}

// CheckRequestTeams verifies that every named team exists in the repository's organization.
func (s Reviews) CheckRequestTeams(ctx context.Context, names []string) error {
	owner, _, _ := strings.Cut(s.Repository, "/")
	_, isOrg, err := organization(ctx, s.Client, owner)
	if err != nil {
		return err
	}
	if !isOrg {
		return errors.New("team reviews require an organization repository")
	}
	teams, err := (Teams{Client: s.Client}).ByName(ctx, owner)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := teams[strings.ToLower(name)]; !ok {
			return fmt.Errorf("team %q is unavailable in organization %s", name, owner)
		}
	}
	return nil
}
