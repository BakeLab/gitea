// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"fmt"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
)

// EditIssueOption wraps around gitea.EditIssueOption which has bad & incosistent semantics.
type EditIssueOption struct {
	Index           int64
	Title           *string
	Body            *string
	Ref             *string
	Milestone       *string
	Deadline        *time.Time
	AddLabels       []string
	RemoveLabels    []string
	AddAssignees    []string
	AddReviewers    []string
	RemoveReviewers []string
	// RemoveAssignees []string // NOTE: with the current go-sdk, clearing assignees is not possible.
}

// Normalizes the options into parameters that can be passed to the sdk.
// the returned value will be nil, when no change to this part of the issue is requested.
func (o EditIssueOption) toSdkOptions(ctx *context.TeaContext, client *gitea.Client) (*gitea.EditIssueOption, *gitea.IssueLabelsOption, *gitea.IssueLabelsOption, error) {
	addLabelOpts, err := ResolveLabelOpts(client, ctx.Owner, ctx.Repo, o.AddLabels)
	if err != nil {
		return nil, nil, nil, err
	}
	rmLabelOpts, err := ResolveLabelOpts(client, ctx.Owner, ctx.Repo, o.RemoveLabels)
	if err != nil {
		return nil, nil, nil, err
	}

	issueOpts := gitea.EditIssueOption{}
	var issueOptsDirty bool
	if o.Title != nil {
		issueOpts.Title = *o.Title
		issueOptsDirty = true
	}
	if o.Body != nil {
		issueOpts.Body = o.Body
		issueOptsDirty = true
	}
	if o.Ref != nil {
		issueOpts.Ref = o.Ref
		issueOptsDirty = true
	}
	if o.Milestone != nil {
		id, err := ResolveMilestoneID(client, ctx.Owner, ctx.Repo, *o.Milestone)
		if err != nil {
			return nil, nil, nil, err
		}
		issueOpts.Milestone = gitea.OptionalInt64(id)
		issueOptsDirty = true
	}
	if o.Deadline != nil {
		issueOpts.Deadline = o.Deadline
		issueOptsDirty = true
		if o.Deadline.IsZero() {
			issueOpts.RemoveDeadline = gitea.OptionalBool(true)
		}
	}
	if len(o.AddAssignees) != 0 {
		issueOpts.Assignees = o.AddAssignees
		issueOptsDirty = true
	}

	if issueOptsDirty {
		return &issueOpts, addLabelOpts, rmLabelOpts, nil
	}
	return nil, addLabelOpts, rmLabelOpts, nil
}

// EditIssue edits an issue and returns the updated issue.
func EditIssue(ctx *context.TeaContext, client *gitea.Client, opts EditIssueOption) (*gitea.Issue, error) {
	if client == nil {
		client = ctx.Login.Client()
	}

	issueOpts, addLabelOpts, rmLabelOpts, err := opts.toSdkOptions(ctx, client)
	if err != nil {
		return nil, err
	}

	if err := ApplyLabelChanges(client, ctx.Owner, ctx.Repo, opts.Index, addLabelOpts, rmLabelOpts); err != nil {
		return nil, err
	}

	var issue *gitea.Issue
	if issueOpts != nil {
		issue, _, err = client.EditIssue(ctx.Owner, ctx.Repo, opts.Index, *issueOpts)
		if err != nil {
			return nil, fmt.Errorf("could not edit issue: %s", err)
		}
	} else {
		issue, _, err = client.GetIssue(ctx.Owner, ctx.Repo, opts.Index)
		if err != nil {
			return nil, fmt.Errorf("could not get issue: %s", err)
		}
	}
	return issue, nil
}
