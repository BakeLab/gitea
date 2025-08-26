// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/charmbracelet/huh"
)

// MergePull interactively creates a PR
func MergePull(ctx *context.TeaContext) error {
	if ctx.LocalRepo == nil {
		return fmt.Errorf("Must specify a PR index")
	}

	branch, _, err := ctx.LocalRepo.TeaGetCurrentBranchNameAndSHA()
	if err != nil {
		return err
	}

	idx, err := getPullIndex(ctx, branch)
	if err != nil {
		return err
	}

	return task.PullMerge(ctx.Login, ctx.Owner, ctx.Repo, idx, gitea.MergePullRequestOption{
		Style:   gitea.MergeStyle(ctx.String("style")),
		Title:   ctx.String("title"),
		Message: ctx.String("message"),
	})
}

// getPullIndex interactively determines the PR index
func getPullIndex(ctx *context.TeaContext, branch string) (int64, error) {
	c := ctx.Login.Client()
	opts := gitea.ListPullRequestsOptions{
		State:       gitea.StateOpen,
		ListOptions: flags.GetListOptions(),
	}
	selected := ""
	loadMoreOption := "PR not found? Load more PRs..."

	// paginated fetch
	var prs []*gitea.PullRequest
	var err error
	for {
		prs, _, err = c.ListRepoPullRequests(ctx.Owner, ctx.Repo, opts)
		if len(prs) == 0 {
			return 0, fmt.Errorf("No open PRs found")
		}
		opts.ListOptions.Page++
		prOptions := make([]string, 0)

		// get the PR indexes where head branch is the current branch
		for _, pr := range prs {
			if pr.Head.Ref == branch {
				prOptions = append(prOptions, fmt.Sprintf("#%d: %s", pr.Index, pr.Title))
			}
		}

		// then get the PR indexes where base branch is the current branch
		for _, pr := range prs {
			// don't add the same PR twice, so `pr.Head.Ref != branch`
			if pr.Base.Ref == branch && pr.Head.Ref != branch {
				prOptions = append(prOptions, fmt.Sprintf("#%d: %s", pr.Index, pr.Title))
			}
		}

		prOptions = append(prOptions, loadMoreOption)

		if err := huh.NewSelect[string]().
			Title("Select a PR to merge:").
			Options(huh.NewOptions(prOptions...)...).
			Value(&selected).
			Filtering(true).
			Run(); err != nil {
			return 0, err
		}

		if selected != loadMoreOption {
			break
		}
	}

	// get the index from the selected option
	before, _, _ := strings.Cut(selected, ":")
	before = strings.TrimPrefix(before, "#")
	idx, err := utils.ArgToIndex(before)
	if err != nil {
		return 0, err
	}

	return idx, nil
}
