// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"strconv"

	gitea "gitea.dev/sdk"
)

// BranchRule is a branch protection rule as Gitea returns it. Protection holds the few settings that
// gt branch protect changes.
type BranchRule struct {
	// Rule is the branch name, or a glob pattern such as release/*, that the rule protects.
	Rule              string `json:"rule"`
	RequiredApprovals int64  `json:"required_approvals"`
	// PushEnabled is false when nobody may push directly. PushRestricted limits it to PushUsers,
	// PushTeams and, when PushDeployKeys is set, deploy keys; otherwise everyone with write access may.
	PushEnabled    bool     `json:"push_enabled"`
	PushRestricted bool     `json:"push_restricted"`
	PushUsers      []string `json:"push_users"`
	PushTeams      []string `json:"push_teams"`
	PushDeployKeys bool     `json:"push_deploy_keys"`
	// ForcePush lets those who may push do it with force, limited to the force push users, teams and,
	// when ForcePushDeployKeys is set, deploy keys when ForcePushRestricted is set.
	ForcePush           bool     `json:"force_push"`
	ForcePushRestricted bool     `json:"force_push_restricted"`
	ForcePushUsers      []string `json:"force_push_users"`
	ForcePushTeams      []string `json:"force_push_teams"`
	ForcePushDeployKeys bool     `json:"force_push_deploy_keys"`
	// MergeRestricted limits who may merge pull requests to MergeUsers and MergeTeams.
	MergeRestricted bool     `json:"merge_restricted"`
	MergeUsers      []string `json:"merge_users"`
	MergeTeams      []string `json:"merge_teams"`
	// StatusChecks must pass before pull requests merge when StatusCheckRequired is set; with none
	// listed, Gitea requires every check reported on the commit to pass.
	StatusCheckRequired bool     `json:"status_check_required"`
	StatusChecks        []string `json:"status_checks"`
	// ApprovalUsers and ApprovalTeams are those whose approvals count, when ApprovalsRestricted is set.
	ApprovalsRestricted           bool     `json:"approvals_restricted"`
	ApprovalUsers                 []string `json:"approval_users"`
	ApprovalTeams                 []string `json:"approval_teams"`
	BlockOnRejected               bool     `json:"block_on_rejected_reviews"`
	BlockOnOfficialReviewRequests bool     `json:"block_on_official_review_requests"`
	BlockOnOutdatedBranch         bool     `json:"block_on_outdated_branch"`
	DismissStaleApprovals         bool     `json:"dismiss_stale_approvals"`
	IgnoreStaleApprovals          bool     `json:"ignore_stale_approvals"`
	RequireSignedCommits          bool     `json:"require_signed_commits"`
	BlockAdminMergeOverride       bool     `json:"block_admin_merge_override"`
	ProtectedFilePatterns         string   `json:"protected_file_patterns"`
	UnprotectedFilePatterns       string   `json:"unprotected_file_patterns"`
	CreatedAt                     string   `json:"created_at"`
	UpdatedAt                     string   `json:"updated_at"`
}

func branchRuleFrom(rule *gitea.BranchProtection) BranchRule {
	list := func(names []string) []string { return append([]string{}, names...) }
	return BranchRule{
		Rule: cmp.Or(rule.RuleName, rule.BranchName), RequiredApprovals: rule.RequiredApprovals,
		PushEnabled: rule.EnablePush, PushRestricted: rule.EnablePushWhitelist, PushUsers: list(rule.PushWhitelistUsernames), PushTeams: list(rule.PushWhitelistTeams), PushDeployKeys: rule.PushWhitelistDeployKeys,
		ForcePush: rule.EnableForcePush, ForcePushRestricted: rule.EnableForcePushAllowlist, ForcePushUsers: list(rule.ForcePushAllowlistUsernames), ForcePushTeams: list(rule.ForcePushAllowlistTeams), ForcePushDeployKeys: rule.ForcePushAllowlistDeployKeys,
		MergeRestricted: rule.EnableMergeWhitelist, MergeUsers: list(rule.MergeWhitelistUsernames), MergeTeams: list(rule.MergeWhitelistTeams),
		StatusCheckRequired: rule.EnableStatusCheck, StatusChecks: statusChecksOf(rule),
		ApprovalsRestricted: rule.EnableApprovalsWhitelist, ApprovalUsers: list(rule.ApprovalsWhitelistUsernames), ApprovalTeams: list(rule.ApprovalsWhitelistTeams),
		BlockOnRejected: rule.BlockOnRejectedReviews, BlockOnOfficialReviewRequests: rule.BlockOnOfficialReviewRequests, BlockOnOutdatedBranch: rule.BlockOnOutdatedBranch,
		DismissStaleApprovals: rule.DismissStaleApprovals, IgnoreStaleApprovals: rule.IgnoreStaleApprovals, RequireSignedCommits: rule.RequireSignedCommits,
		BlockAdminMergeOverride: rule.BlockAdminMergeOverride,
		ProtectedFilePatterns:   rule.ProtectedFilePatterns, UnprotectedFilePatterns: rule.UnprotectedFilePatterns,
		CreatedAt: timestamp(rule.Created), UpdatedAt: timestamp(rule.Updated),
	}
}

// statusChecksOf returns the checks a rule requires: none when it does not require them, whatever
// contexts it still holds.
func statusChecksOf(rule *gitea.BranchProtection) []string {
	if !rule.EnableStatusCheck {
		return []string{}
	}
	return append([]string{}, rule.StatusCheckContexts...)
}

// Protections lists the protection rules of a repository, in the order that Gitea applies them.
// Gitea answers all of them at once and ignores page and limit.
func (s Branches) Protections(ctx context.Context, repo string) ([]BranchRule, error) {
	rules, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.BranchProtection, *gitea.Response, error) {
		return c.Repositories.ListBranchProtections(ctx, owner, name, gitea.ListBranchProtectionsOptions{})
	})
	if err != nil {
		return nil, repositoryItemError(ctx, s.Client, repo, err, "repository", repo)
	}
	return convertAll(rules, branchRuleFrom), nil
}

// Protection returns the rule named rule, a branch name or the pattern it was made with.
func (s Branches) Protection(ctx context.Context, repo, rule string) (BranchRule, error) {
	found, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.BranchProtection, *gitea.Response, error) {
		return c.Repositories.GetBranchProtection(ctx, owner, name, rule)
	})
	if err != nil {
		return BranchRule{}, repositoryItemError(ctx, s.Client, repo, err, "protection rule", strconv.Quote(rule))
	}
	return branchRuleFrom(found), nil
}
