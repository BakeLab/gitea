// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"

	"github.com/charmbracelet/huh"
)

// CreatePull interactively creates a PR
func CreatePull(ctx *context.TeaContext) (err error) {
	var (
		base, head           string
		allowMaintainerEdits = true
	)

	// owner, repo
	if ctx.Owner, ctx.Repo, err = promptRepoSlug(ctx.Owner, ctx.Repo); err != nil {
		return err
	}

	// base
	if base, err = task.GetDefaultPRBase(ctx.Login, ctx.Owner, ctx.Repo); err != nil {
		return err
	}

	var headOwner, headBranch string
	validator := huh.ValidateNotEmpty()
	if ctx.LocalRepo != nil {
		headOwner, headBranch, err = task.GetDefaultPRHead(ctx.LocalRepo)
		if err == nil {
			validator = func(string) error { return nil }
		}
	}

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Target branch:").
				Value(&base).
				Validate(huh.ValidateNotEmpty()),

			huh.NewInput().
				Title("Source repo owner:").
				Value(&headOwner),

			huh.NewInput().
				Title("Source branch:").
				Value(&headBranch).
				Validate(validator),

			huh.NewConfirm().
				Title("Allow maintainers to push to the base branch:").
				Value(&allowMaintainerEdits),
		),
	).Run(); err != nil {
		return err
	}

	head = task.GetHeadSpec(headOwner, headBranch, ctx.Owner)

	opts := gitea.CreateIssueOption{Title: task.GetDefaultPRTitle(head)}
	if err = promptIssueProperties(ctx.Login, ctx.Owner, ctx.Repo, &opts); err != nil {
		return err
	}

	return task.CreatePull(
		ctx,
		base,
		head,
		&allowMaintainerEdits,
		&opts)
}
