// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"
	"regexp"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/context"
	local_git "gitea.dev/tea/modules/git"
	"gitea.dev/tea/modules/utils"
)

var (
	spaceRegex  = regexp.MustCompile(`[\s_-]+`)
	noSpace     = regexp.MustCompile(`^[^a-zA-Z\s]*`)
	consecutive = regexp.MustCompile(`[\s]{2,}`)
)

// CreatePull creates a PR in the given repo and returns the created PR
func CreatePull(requestCtx stdctx.Context, ctx *context.TeaContext, base, head string, allowMaintainerEdits *bool, opts *gitea.CreateIssueOption) (*gitea.PullRequest, error) {
	var err error

	// default is default branch
	if len(base) == 0 {
		base, err = GetDefaultPRBase(requestCtx, ctx.Login, ctx.Owner, ctx.Repo)
		if err != nil {
			return nil, err
		}
	}

	// default is current one
	if len(head) == 0 {
		if ctx.LocalRepo == nil {
			return nil, fmt.Errorf("no local git repo detected, please specify head branch")
		}
		headOwner, headBranch, err := GetDefaultPRHead(ctx.LocalRepo)
		if err != nil {
			return nil, err
		}

		head = GetHeadSpec(headOwner, headBranch, ctx.Owner)
	}

	// head & base may not be the same
	if head == base {
		return nil, fmt.Errorf("can't create PR from %s to %s", head, base)
	}

	// default is head branch name
	if len(opts.Title) == 0 {
		opts.Title = GetDefaultPRTitle(head)
	}
	// title is required
	if len(opts.Title) == 0 {
		return nil, fmt.Errorf("title is required")
	}

	client := ctx.Login.Client()

	pr, _, err := client.PullRequests.CreatePullRequest(requestCtx, ctx.Owner, ctx.Repo, gitea.CreatePullRequestOption{
		Head:      head,
		Base:      base,
		Title:     opts.Title,
		Body:      opts.Body,
		Assignees: opts.Assignees,
		Labels:    opts.Labels,
		Milestone: opts.Milestone,
		Deadline:  opts.Deadline,
	})
	if err != nil {
		return nil, fmt.Errorf("could not create PR from %s to %s:%s: %s", head, ctx.Owner, base, err)
	}

	if allowMaintainerEdits != nil && pr.AllowMaintainerEdit != *allowMaintainerEdits {
		pr, _, err = client.PullRequests.EditPullRequest(requestCtx, ctx.Owner, ctx.Repo, pr.Index, gitea.EditPullRequestOption{
			AllowMaintainerEdit: allowMaintainerEdits,
		})
		if err != nil {
			return nil, fmt.Errorf("could not enable maintainer edit on pull: %v", err)
		}
	}

	return pr, nil
}

// GetDefaultPRBase retrieves the default base branch for the given repo
func GetDefaultPRBase(requestCtx stdctx.Context, login *config.Login, owner, repo string) (string, error) {
	meta, _, err := login.Client().Repositories.GetRepo(requestCtx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("could not fetch repo meta: %s", err)
	}
	return meta.DefaultBranch, nil
}

// GetDefaultPRHead uses the currently checked out branch, tries to find a remote
// that has a branch with the same name, and extracts the owner from its URL.
// If no remote matches, owner is empty, meaning same as head repo owner.
func GetDefaultPRHead(localRepo *local_git.TeaRepo) (owner, branch string, err error) {
	var sha string
	if branch, sha, err = localRepo.TeaGetCurrentBranchNameAndSHA(); err != nil {
		return
	}

	remote, err := localRepo.TeaFindBranchRemote(branch, sha)
	if err != nil {
		err = fmt.Errorf("could not determine remote for current branch: %s", err)
		return
	}

	if remote == nil {
		// if no remote branch is found for the local branch,
		// we leave owner empty, meaning "use same repo as head" to gitea.
		return
	}

	url, err := local_git.ParseURL(remote.Config().URLs[0])
	if err != nil {
		return
	}
	owner, _ = utils.GetOwnerAndRepo(url.Path, "")
	return
}

// GetHeadSpec creates a head string as expected by gitea API
func GetHeadSpec(owner, branch, baseOwner string) string {
	if len(owner) != 0 && owner != baseOwner {
		return fmt.Sprintf("%s:%s", owner, branch)
	}
	return branch
}

// GetDefaultPRTitle transforms a string like a branchname to a readable text
func GetDefaultPRTitle(header string) string {
	// Extract the part after the last colon in the input string
	colonIndex := strings.LastIndex(header, ":")
	if colonIndex != -1 {
		header = header[colonIndex+1:]
	}

	title := noSpace.ReplaceAllString(header, "")
	title = spaceRegex.ReplaceAllString(title, " ")
	title = strings.TrimSpace(title)
	title = strings.Title(strings.ToLower(title))
	title = consecutive.ReplaceAllString(title, " ")

	return title
}

// CreateAgitFlowPull creates a agit flow PR in the given repo and prints the result
func CreateAgitFlowPull(requestCtx stdctx.Context, ctx *context.TeaContext, remote, head, base, topic string,
	opts *gitea.CreateIssueOption,
	callback func(string) (string, error),
) (err error) {
	// default is default branch
	if len(base) == 0 {
		base, err = GetDefaultPRBase(requestCtx, ctx.Login, ctx.Owner, ctx.Repo)
		if err != nil {
			return err
		}
	}

	// default is current one
	if len(head) == 0 {
		if ctx.LocalRepo == nil {
			return fmt.Errorf("no local git repo detected, please specify topic branch")
		}
		headOwner, headBranch, err := GetDefaultPRHead(ctx.LocalRepo)
		if err != nil {
			return err
		}

		head = GetHeadSpec(headOwner, headBranch, ctx.Owner)
	}

	if len(remote) == 0 {
		return fmt.Errorf("remote is required for agit flow PR")
	}

	if len(topic) == 0 {
		topic = head
	}

	if head == base || topic == base {
		return fmt.Errorf("can't create PR from %s to %s", topic, base)
	}

	// default is head branch name
	if len(opts.Title) == 0 {
		opts.Title = GetDefaultPRTitle(head)
	}
	// title is required
	if len(opts.Title) == 0 {
		return fmt.Errorf("title is required")
	}

	localRepo, err := local_git.RepoForWorkdir()
	if err != nil {
		return err
	}

	url, err := localRepo.RemoteURL(remote)
	if err != nil {
		return err
	}

	auth, err := local_git.GetAuthForURL(url, ctx.Login.GetAccessToken(), ctx.Login.SSHKey, callback)
	if err != nil {
		return err
	}

	return localRepo.PushToCreatAgitFlowPR(remote, head, base, topic, opts.Title, opts.Body, auth)
}
