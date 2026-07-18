// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/modules/context"
)

// EditPull edits a pull request and returns the updated pull request.
func EditPull(requestCtx stdctx.Context, ctx *context.TeaContext, client *gitea.Client, opts EditIssueOption) (*gitea.PullRequest, error) {
	if client == nil {
		client = ctx.Login.Client()
	}

	addLabelOpts, err := ResolveLabelOpts(requestCtx, client, ctx.Owner, ctx.Repo, opts.AddLabels)
	if err != nil {
		return nil, err
	}
	rmLabelOpts, err := ResolveLabelOpts(requestCtx, client, ctx.Owner, ctx.Repo, opts.RemoveLabels)
	if err != nil {
		return nil, err
	}
	addAssigneeOpts := ResolveAssigneeOpts(opts.AddAssignees)
	rmAssigneeOpts := ResolveAssigneeOpts(opts.RemoveAssignees)

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
		id, err := ResolveMilestoneID(requestCtx, client, ctx.Owner, ctx.Repo, *opts.Milestone)
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
	opts.SetAssignees = cleanAssignees(opts.SetAssignees)
	if len(opts.SetAssignees) != 0 {
		prOpts.Assignees = opts.SetAssignees
		prOptsDirty = true
	}

	if err := ApplyLabelChanges(requestCtx, client, ctx.Owner, ctx.Repo, opts.Index, addLabelOpts, rmLabelOpts); err != nil {
		return nil, err
	}

	if err := ApplyAssigneeChanges(requestCtx, client, ctx.Owner, ctx.Repo, opts.Index, addAssigneeOpts, rmAssigneeOpts); err != nil {
		return nil, err
	}

	if err := ApplyReviewerChanges(requestCtx, client, ctx.Owner, ctx.Repo, opts.Index, opts.AddReviewers, opts.RemoveReviewers); err != nil {
		return nil, err
	}

	var pr *gitea.PullRequest
	if prOptsDirty {
		pr, _, err = client.PullRequests.EditPullRequest(requestCtx, ctx.Owner, ctx.Repo, opts.Index, prOpts)
		if err != nil {
			return nil, fmt.Errorf("could not edit pull request: %s", err)
		}
	} else {
		pr, _, err = client.PullRequests.GetPullRequest(requestCtx, ctx.Owner, ctx.Repo, opts.Index)
		if err != nil {
			return nil, fmt.Errorf("could not get pull request: %s", err)
		}
	}
	return pr, nil
}
