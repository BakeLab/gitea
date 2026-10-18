// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Branch describes a remote branch, its last commit and its protection.
type Branch struct {
	Name              string `json:"name"`
	SHA               string `json:"sha"`
	Message           string `json:"message"`
	UpdatedAt         string `json:"updated_at"`
	Protected         bool   `json:"protected"`
	RequiredApprovals int64  `json:"required_approvals"`
	CanPush           bool   `json:"can_push"`
	CanMerge          bool   `json:"can_merge"`
}

func branchFrom(branch *gitea.Branch) Branch {
	result := Branch{
		Name: branch.Name, Protected: branch.Protected, RequiredApprovals: branch.RequiredApprovals,
		CanPush: branch.UserCanPush, CanMerge: branch.UserCanMerge,
	}
	if branch.Commit != nil {
		result.SHA, result.Message, result.UpdatedAt = branch.Commit.ID, branch.Commit.Message, timestamp(branch.Commit.Timestamp)
	}
	return result
}

// Branches operates on remote branches and protection rules.
type Branches struct{ Client *transport.Client }

// ListPage lists a page of branches, with whether more follow and how many there are.
func (s Branches) ListPage(ctx context.Context, repo string, limit, page int) (Page[Branch], error) {
	window, err := listPage(limit, page, func(limit, page int) ([]*gitea.Branch, http.Header, error) {
		var headers http.Header
		branches, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Branch, *gitea.Response, error) {
			branches, resp, err := c.Repositories.ListRepoBranches(ctx, owner, name, gitea.ListRepoBranchesOptions{ListOptions: listOptions(limit, page)})
			headers = headerOf(resp)
			return branches, resp, err
		})
		return branches, headers, err
	})
	if err != nil {
		return Page[Branch]{}, err
	}
	return convertPage(window, branchFrom), nil
}

// View retrieves a remote branch.
func (s Branches) View(ctx context.Context, repo, branch string) (Branch, error) {
	item, err := repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.GetRepoBranch(ctx, owner, name, branch)
	})
	return item, Classify(err, "branch", branch, repo)
}

// Create creates a branch from source, a branch, tag or commit, or from the default branch.
func (s Branches) Create(ctx context.Context, repo, branch, source string) (Branch, error) {
	if source == "" {
		return repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
			return c.Repositories.CreateBranch(ctx, owner, name, gitea.CreateBranchOption{BranchName: branch})
		})
	}
	if _, _, err := splitRepository(repo); err != nil {
		return Branch{}, err
	}
	// TODO(sdk): CreateBranchOption lacks old_ref_name, which also accepts tags and commits.
	var created gitea.Branch
	payload := map[string]string{"new_branch_name": branch, "old_ref_name": source}
	if err := sendJSON(ctx, s.Client, http.MethodPost, "repos/"+repo+"/branches", payload, &created); err != nil {
		// Gitea answers an unknown source with a 500 instead of a 404.
		if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusInternalServerError && strings.Contains(apiErr.Message, "object does not exist") {
			return Branch{}, &NotFoundError{Kind: "branch, tag or commit", Key: strconv.Quote(source), Scope: repo, Err: err}
		}
		return Branch{}, err
	}
	return branchFrom(&created), nil
}

// Rename renames a remote branch.
func (s Branches) Rename(ctx context.Context, repo, oldName, newName string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.RenameRepoBranch(ctx, owner, name, oldName, gitea.RenameRepoBranchOption{Name: newName})
		return resp, err
	})
}

// Delete removes a remote branch.
func (s Branches) Delete(ctx context.Context, repo, branch string) error {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.DeleteRepoBranch(ctx, owner, name, branch)
		return resp, err
	})
	if IsNotFound(err) {
		if _, repoErr := (Repositories{Client: s.Client}).get(ctx, repo); repoErr != nil {
			return repoErr
		}
	}
	return Classify(err, "branch", branch, repo)
}

// Protection is a branch protection rule.
type Protection struct {
	// Rule is the branch name, or a glob pattern such as release/*, that the rule protects.
	Rule              string `json:"rule"`
	RequiredApprovals int64  `json:"required_approvals"`
	// PushUsers and PushTeams may push directly; without them nobody can.
	PushUsers []string `json:"push_users"`
	PushTeams []string `json:"push_teams"`
	// StatusChecks must pass before pull requests merge.
	StatusChecks          []string `json:"status_checks"`
	BlockOnRejected       bool     `json:"block_on_rejected_reviews"`
	DismissStaleApprovals bool     `json:"dismiss_stale_approvals"`
}

// Protect adds a protection rule. It reports false without a change when the rule exists already.
func (s Branches) Protect(ctx context.Context, repo string, rule Protection) (bool, error) {
	push := len(rule.PushUsers)+len(rule.PushTeams) > 0
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.CreateBranchProtection(ctx, owner, name, gitea.CreateBranchProtectionOption{
			RuleName: rule.Rule, RequiredApprovals: rule.RequiredApprovals,
			EnablePush: push, EnablePushWhitelist: push, PushWhitelistUsernames: rule.PushUsers, PushWhitelistTeams: rule.PushTeams,
			EnableStatusCheck: len(rule.StatusChecks) > 0, StatusCheckContexts: rule.StatusChecks,
			BlockOnRejectedReviews: rule.BlockOnRejected, DismissStaleApprovals: rule.DismissStaleApprovals,
		})
		return resp, err
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && strings.Contains(strings.ToLower(apiErr.Message), "already exist") {
		return false, nil
	}
	return err == nil, err
}

// Unprotect removes the protection rule named rule. It reports false when there is none.
func (s Branches) Unprotect(ctx context.Context, repo, rule string) (bool, error) {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteBranchProtection(ctx, owner, name, rule)
	})
	if IsNotFound(err) {
		if _, err := (Repositories{Client: s.Client}).get(ctx, repo); err != nil {
			return false, err
		}
		return false, nil
	}
	return err == nil, err
}
