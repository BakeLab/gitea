// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// Branch describes a remote branch and its protection.
type Branch struct {
	Name              string `json:"name"`
	SHA               string `json:"sha"`
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
		result.SHA = branch.Commit.ID
	}
	return result
}

// Branches operates on remote branches and protection rules.
type Branches struct{ Client *transport.Client }

// List returns a page of remote branches.
func (s Branches) List(ctx context.Context, repo string, limit, page int) ([]Branch, error) {
	branches, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.ListRepoBranches(ctx, owner, name, gitea.ListRepoBranchesOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(branches, branchFrom), nil
}

// View retrieves a remote branch.
func (s Branches) View(ctx context.Context, repo, branch string) (Branch, error) {
	return repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.GetRepoBranch(ctx, owner, name, branch)
	})
}

// Create creates a branch from the supplied source or repository default.
func (s Branches) Create(ctx context.Context, repo, branch, source string) (Branch, error) {
	return repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.CreateBranch(ctx, owner, name, gitea.CreateBranchOption{BranchName: branch, OldBranchName: source})
	})
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
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.DeleteRepoBranch(ctx, owner, name, branch)
		return resp, err
	})
}

// Protect blocks direct pushes to the branch and requires one approving review.
func (s Branches) Protect(ctx context.Context, repo, branch string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.CreateBranchProtection(ctx, owner, name, gitea.CreateBranchProtectionOption{
			BranchName: branch, RuleName: branch, RequiredApprovals: 1,
		})
		return resp, err
	})
}

// Unprotect removes the named branch protection rule.
func (s Branches) Unprotect(ctx context.Context, repo, branch string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteBranchProtection(ctx, owner, name, branch)
	})
}
