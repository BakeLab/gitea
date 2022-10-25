// Copyright 2022 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package task

import (
	"fmt"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
)

// EditIssueOption wraps around gitea.EditIssueOption which has bad & incosistent semantics.
type EditIssueOption struct {
	Index        int64
	Title        *string
	Body         *string
	Ref          *string
	Milestone    *string
	Deadline     *time.Time
	AddLabels    []string
	RemoveLabels []string
	AddAssignees []string
	// RemoveAssignees []string // NOTE: with the current go-sdk, clearing assignees is not possible.
}

// Normalizes the options into parameters that can be passed to the sdk.
// the returned value will be nil, when no change to this part of the issue is requested.
func (o EditIssueOption) toSdkOptions(ctx *context.TeaContext, client *gitea.Client) (*gitea.EditIssueOption, *gitea.IssueLabelsOption, *gitea.IssueLabelsOption, error) {
	// labels have a separate API call, so they get their own options.
	var addLabelOpts, rmLabelOpts *gitea.IssueLabelsOption
	if o.AddLabels != nil && len(o.AddLabels) != 0 {
		ids, err := ResolveLabelNames(client, ctx.Owner, ctx.Repo, o.AddLabels)
		if err != nil {
			return nil, nil, nil, err
		}
		addLabelOpts = &gitea.IssueLabelsOption{Labels: ids}
	}

	if o.RemoveLabels != nil && len(o.RemoveLabels) != 0 {
		ids, err := ResolveLabelNames(client, ctx.Owner, ctx.Repo, o.RemoveLabels)
		if err != nil {
			return nil, nil, nil, err
		}
		rmLabelOpts = &gitea.IssueLabelsOption{Labels: ids}
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
		if *o.Milestone == "" {
			issueOpts.Milestone = gitea.OptionalInt64(0)
		} else {
			ms, _, err := client.GetMilestoneByName(ctx.Owner, ctx.Repo, *o.Milestone)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("Milestone '%s' not found", *o.Milestone)
			}
			issueOpts.Milestone = &ms.ID
		}
		issueOptsDirty = true
	}
	if o.Deadline != nil {
		issueOpts.Deadline = o.Deadline
		issueOptsDirty = true
		if o.Deadline.IsZero() {
			issueOpts.RemoveDeadline = gitea.OptionalBool(true)
		}
	}
	if o.AddAssignees != nil && len(o.AddAssignees) != 0 {
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

	if rmLabelOpts != nil {
		// NOTE: as of 1.17, there is no API to remove multiple labels at once.
		for _, id := range rmLabelOpts.Labels {
			_, err := client.DeleteIssueLabel(ctx.Owner, ctx.Repo, opts.Index, id)
			if err != nil {
				return nil, fmt.Errorf("could not remove labels: %s", err)
			}
		}
	}

	if addLabelOpts != nil {
		_, _, err := client.AddIssueLabels(ctx.Owner, ctx.Repo, opts.Index, *addLabelOpts)
		if err != nil {
			return nil, fmt.Errorf("could not add labels: %s", err)
		}
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
