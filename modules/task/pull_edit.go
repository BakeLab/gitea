// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
)

// EditPull edits a pull request and returns the updated pull request.
func EditPull(ctx *context.TeaContext, client *gitea.Client, opts EditIssueOption) (*gitea.PullRequest, error) {
	if client == nil {
		client = ctx.Login.Client()
	}

	addLabelOpts, err := ResolveLabelOpts(client, ctx.Owner, ctx.Repo, opts.AddLabels)
	if err != nil {
		return nil, err
	}
	rmLabelOpts, err := ResolveLabelOpts(client, ctx.Owner, ctx.Repo, opts.RemoveLabels)
	if err != nil {
		return nil, err
	}

	prOpts := gitea.EditPullRequestOption{}
	var prOptsDirty bool
	if opts.Title != nil {
		prOpts.Title = *opts.Title
		prOptsDirty = true
	}
	if opts.Body != nil {
		prOpts.Body = opts.Body
		prOptsDirty = true
	}
	if opts.Milestone != nil {
		id, err := ResolveMilestoneID(client, ctx.Owner, ctx.Repo, *opts.Milestone)
		if err != nil {
			return nil, err
		}
		prOpts.Milestone = id
		prOptsDirty = true
	}
	if opts.Deadline != nil {
		prOpts.Deadline = opts.Deadline
		prOptsDirty = true
		if opts.Deadline.IsZero() {
			prOpts.RemoveDeadline = gitea.OptionalBool(true)
		}
	}
	if len(opts.AddAssignees) != 0 {
		prOpts.Assignees = opts.AddAssignees
		prOptsDirty = true
	}

	if err := ApplyLabelChanges(client, ctx.Owner, ctx.Repo, opts.Index, addLabelOpts, rmLabelOpts); err != nil {
		return nil, err
	}

	if err := ApplyReviewerChanges(client, ctx.Owner, ctx.Repo, opts.Index, opts.AddReviewers, opts.RemoveReviewers); err != nil {
		return nil, err
	}

	var pr *gitea.PullRequest
	if prOptsDirty {
		pr, _, err = client.EditPullRequest(ctx.Owner, ctx.Repo, opts.Index, prOpts)
		if err != nil {
			return nil, fmt.Errorf("could not edit pull request: %s", err)
		}
	} else {
		pr, _, err = client.GetPullRequest(ctx.Owner, ctx.Repo, opts.Index)
		if err != nil {
			return nil, fmt.Errorf("could not get pull request: %s", err)
		}
	}
	return pr, nil
}
