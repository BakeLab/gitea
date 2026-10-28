// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
		// The commit time carries the time zone of each committer, so branches of one repository
		// would compare and read in different zones; UTC gives them one.
		result.SHA, result.Message, result.UpdatedAt = branch.Commit.ID, branch.Commit.Message, timestamp(branch.Commit.Timestamp.UTC())
	}
	return result
}

// Branches operates on remote branches and protection rules.
type Branches struct{ Client *transport.Client }

// ListPage lists a page of branches, with whether more follow and how many there are.
func (s Branches) ListPage(ctx context.Context, repo string, limit, page int) (Page[Branch], error) {
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Branch, http.Header, error) {
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

// Find retrieves a remote branch, naming repo instead when that is what is missing.
func (s Branches) Find(ctx context.Context, repo, branch string) (Branch, error) {
	item, err := s.get(ctx, repo, branch)
	return item, repositoryItemError(ctx, s.Client, repo, err, "branch", strconv.Quote(branch))
}

// View retrieves a remote branch of a repository the caller knows to exist: a missing
// repository reads as a missing branch, without the request of Find that tells them apart.
func (s Branches) View(ctx context.Context, repo, branch string) (Branch, error) {
	item, err := s.get(ctx, repo, branch)
	return item, Classify(err, "branch", strconv.Quote(branch), repo)
}

func (s Branches) get(ctx context.Context, repo, branch string) (Branch, error) {
	return repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.GetRepoBranch(ctx, owner, name, branch)
	})
}

// RequireAll fails unless every branch exists in repo, naming each one that does not, or repo
// when that is what is missing.
func (s Branches) RequireAll(ctx context.Context, repo string, branches []string) error {
	failures := make([]error, len(branches))
	_ = concurrently(len(branches), func(i int) error {
		_, failures[i] = s.get(ctx, repo, branches[i])
		return nil
	})
	// One lookup of the repository tells for every branch whether it is what is missing.
	if slices.ContainsFunc(failures, IsNotFound) {
		if err := missingRepository(ctx, s.Client, repo); err != nil {
			return err
		}
	}
	for i, err := range failures {
		failures[i] = Classify(err, "branch", strconv.Quote(branches[i]), repo)
	}
	return errors.Join(failures...)
}

// Create creates a branch from source, a branch, tag or commit, or from the default branch.
func (s Branches) Create(ctx context.Context, repo, branch, source string) (Branch, error) {
	// An existing branch is refused with HTTP 409 "The branch already exists", and one that a
	// protection rule covers with a 500 that carries Git's output; a 404 can be the repository's.
	exists := func(err error) error {
		if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusInternalServerError && strings.Contains(apiErr.Message, "protected branch") {
			return &RefusedError{Text: fmt.Sprintf("a protection rule of %s refuses to create branch %s", repo, branch), Err: err}
		}
		if IsNotFound(err) {
			if repoErr := missingRepository(ctx, s.Client, repo); repoErr != nil {
				return repoErr
			}
		}
		if conflict, ok := errors.AsType[*ConflictError](Classify(err, "branch", strconv.Quote(branch), repo)); ok {
			return conflict
		}
		return err
	}
	item, err := repoConvert(ctx, s.Client, repo, branchFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Branch, *gitea.Response, error) {
		return c.Repositories.CreateBranch(ctx, owner, name, gitea.CreateBranchOption{BranchName: branch, OldRefName: source})
	})
	// Gitea answers an unknown source with a 500 instead of a 404.
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && source != "" && apiErr.Status == http.StatusInternalServerError && strings.Contains(apiErr.Message, "object does not exist") {
		return Branch{}, &NotFoundError{Kind: "branch, tag or commit", Key: strconv.Quote(source), Scope: repo, Err: err}
	}
	return item, exists(err)
}

// Rename renames a remote branch.
func (s Branches) Rename(ctx context.Context, repo, oldName, newName string) error {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.RenameRepoBranch(ctx, owner, name, oldName, gitea.RenameRepoBranchOption{Name: newName})
		return resp, err
	})
	if IsNotFound(err) {
		return repositoryItemError(ctx, s.Client, repo, err, "branch", strconv.Quote(oldName))
	}
	// A refusal such as an existing branch is about the new name.
	return Classify(err, "branch", strconv.Quote(newName), repo)
}

// Delete removes a remote branch.
func (s Branches) Delete(ctx context.Context, repo, branch string) error {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.Repositories.DeleteRepoBranch(ctx, owner, name, branch)
		return resp, err
	})
	return repositoryItemError(ctx, s.Client, repo, err, "branch", strconv.Quote(branch))
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

// DroppedPushersError reports the users and teams that Gitea left out of a new protection rule,
// which it does for those that cannot write to the repository.
type DroppedPushersError struct {
	Repository, Rule string
	Users, Teams     []string
	// Nobody is set when Gitea dropped every pusher: the rule then lets nobody push.
	Nobody bool
	// Updated is set when an existing rule was changed rather than a new one created.
	Updated bool
}

func (e *DroppedPushersError) Error() string {
	names := slices.Clone(e.Users)
	org, _, _ := strings.Cut(e.Repository, "/")
	for _, team := range e.Teams {
		names = append(names, "team "+org+"/"+team)
	}
	verb := "created"
	if e.Updated {
		verb = "updated"
	}
	text := fmt.Sprintf("the rule was %s without %s among those who may push, as only users and teams with write access to %s can push", verb, strings.Join(names, ", "), e.Repository)
	if e.Nobody {
		text += "; nobody can push to " + e.Rule + " now"
	}
	// gt branch protect updates the existing rule with the pushers it is given again.
	return text + "; to retry, grant them write access and protect again with the same --allow-push"
}

// Protect adds a protection rule. It reports false without a change when the rule exists already,
// and a *DroppedPushersError when the rule leaves out some of its pushers.
func (s Branches) Protect(ctx context.Context, repo string, rule Protection) (bool, error) {
	push := len(rule.PushUsers)+len(rule.PushTeams) > 0
	var created *gitea.BranchProtection
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		protection, resp, err := c.Repositories.CreateBranchProtection(ctx, owner, name, gitea.CreateBranchProtectionOption{
			RuleName: rule.Rule, RequiredApprovals: rule.RequiredApprovals,
			EnablePush: push, EnablePushWhitelist: push, PushWhitelistUsernames: rule.PushUsers, PushWhitelistTeams: rule.PushTeams,
			EnableStatusCheck: len(rule.StatusChecks) > 0, StatusCheckContexts: rule.StatusChecks,
			BlockOnRejectedReviews: rule.BlockOnRejected, DismissStaleApprovals: rule.DismissStaleApprovals,
		})
		created = protection
		return resp, err
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && strings.Contains(strings.ToLower(apiErr.Message), "already exist") {
		return false, nil
	}
	if IsNotFound(err) {
		return false, Classify(err, "repository", repo, "")
	}
	if err != nil {
		return false, Classify(err, "protection rule", strconv.Quote(rule.Rule), repo)
	}
	if created == nil {
		return true, nil
	}
	return true, droppedPushers(repo, rule, created, false)
}

// droppedPushers returns a *DroppedPushersError when saved, the rule as Gitea stored it, leaves
// out pushers that rule asked for.
func droppedPushers(repo string, rule Protection, saved *gitea.BranchProtection, updated bool) error {
	dropped := &DroppedPushersError{
		Repository: repo, Rule: rule.Rule, Updated: updated,
		Users: missingNames(rule.PushUsers, saved.PushWhitelistUsernames), Teams: missingNames(rule.PushTeams, saved.PushWhitelistTeams),
	}
	if len(dropped.Users)+len(dropped.Teams) > 0 {
		dropped.Nobody = len(saved.PushWhitelistUsernames)+len(saved.PushWhitelistTeams) == 0
		return dropped
	}
	return nil
}

// ProtectionChanges selects the settings of a Protection that EditProtection changes.
type ProtectionChanges struct {
	RequiredApprovals, Pushers, StatusChecks, BlockOnRejected, DismissStaleApprovals bool
}

// EditProtection changes the settings of the existing rule named rule.Rule that changes selects,
// and keeps the others. Pushers replace those who may push directly. It fails with a
// *DroppedPushersError when the rule leaves out some of its pushers.
func (s Branches) EditProtection(ctx context.Context, repo string, rule Protection, changes ProtectionChanges) error {
	var option gitea.EditBranchProtectionOption
	if changes.RequiredApprovals {
		option.RequiredApprovals = &rule.RequiredApprovals
	}
	if changes.Pushers {
		push := len(rule.PushUsers)+len(rule.PushTeams) > 0
		option.EnablePush, option.EnablePushWhitelist = &push, &push
		// Gitea keeps a list sent as null, so lists the pushers leave empty are sent empty.
		option.PushWhitelistUsernames, option.PushWhitelistTeams = append([]string{}, rule.PushUsers...), append([]string{}, rule.PushTeams...)
	}
	if changes.StatusChecks {
		check := len(rule.StatusChecks) > 0
		option.EnableStatusCheck, option.StatusCheckContexts = &check, rule.StatusChecks
	}
	if changes.BlockOnRejected {
		option.BlockOnRejectedReviews = &rule.BlockOnRejected
	}
	if changes.DismissStaleApprovals {
		option.DismissStaleApprovals = &rule.DismissStaleApprovals
	}
	var saved *gitea.BranchProtection
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		protection, resp, err := c.Repositories.EditBranchProtection(ctx, owner, name, rule.Rule, option)
		saved = protection
		return resp, err
	})
	if err != nil {
		return Classify(err, "protection rule", strconv.Quote(rule.Rule), repo)
	}
	if !changes.Pushers || saved == nil {
		return nil
	}
	return droppedPushers(repo, rule, saved, true)
}

// missingNames lists the names of requested that granted lacks, ignoring case as Gitea does.
func missingNames(requested, granted []string) []string {
	var missing []string
	for _, name := range requested {
		if !slices.ContainsFunc(granted, func(g string) bool { return strings.EqualFold(g, name) }) {
			missing = append(missing, name)
		}
	}
	return missing
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
