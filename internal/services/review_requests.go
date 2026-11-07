// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
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
	item, err := Pulls(s).View(ctx, n)
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
	// Gitea refuses an unknown user with a 404 that does not say whether other requests were applied.
	users := append(append([]string{}, changes.Add.Users...), changes.Remove.Users...)
	if err := Pulls(s).CheckUsers(ctx, nil, users); err != nil {
		return err
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
			err = s.requestError(ctx, n, err)
			if mutated {
				return fmt.Errorf("reviewer removals succeeded, but adding reviewers failed: %w", err)
			}
			// Gitea checks the pull request and every name before it applies any request.
			if _, ok := errors.AsType[*NotFoundError](err); ok {
				return err
			}
			return fmt.Errorf("review request update failed; Gitea may have applied some requests: %w", err)
		}
		mutated = true
	}
	return nil
}

// requestError names what a refused review request change on pull request n is missing: a
// reviewer or team, which Gitea names as "user doesn't exist: NAME" (a reviewer named by email
// is left to it) or "team doesn't exist: NAME", or else the pull request or the repository.
func (s Reviews) requestError(ctx context.Context, n int64, err error) error {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok {
		return err
	}
	if name, found := strings.CutPrefix(apiErr.Message, "user doesn't exist: "); found {
		return &NotFoundError{Kind: "user", Key: strconv.Quote(name), Err: err}
	}
	if name, found := strings.CutPrefix(apiErr.Message, "team doesn't exist: "); found {
		owner, _, _ := strings.Cut(s.Repository, "/")
		return &NotFoundError{Kind: "team", Key: strconv.Quote(name), Scope: owner, Err: err}
	}
	if apiErr.Status == http.StatusNotFound && (apiErr.Message == "not found" || strings.HasPrefix(apiErr.Message, "pull request does not exist")) {
		return pullReadError(ctx, s.Client, s.Repository, err, n)
	}
	return err
}

// CheckRequestTeams verifies that every named team exists in the repository's organization. Gitea
// lets only organization members list teams, though outside collaborators and the pull request
// author may request reviews; when the lookup is forbidden, or the organization is hidden from the
// caller (a private organization answers 404 to non-members), the server checks the names, as for gh.
func (s Reviews) CheckRequestTeams(ctx context.Context, names []string) error {
	owner, _, _ := strings.Cut(s.Repository, "/")
	_, isOrg, err := organization(ctx, s.Client, owner)
	if IsForbidden(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isOrg {
		// A private organization answers 404 to non-members, like a name nobody owns; a user that
		// exists is certainly not an organization.
		_, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
			return c.Users.GetUserInfo(ctx, owner)
		})
		if IsNotFound(err) || IsForbidden(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return errors.New("team reviews require an organization repository")
	}
	teams, err := (Teams{Client: s.Client}).ByName(ctx, owner)
	if IsForbidden(err) || IsNotFound(err) {
		return nil
	}
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
