// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"slices"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"

	"github.com/AlecAivazis/survey/v2"
)

// EditIssue interactively edits an issue
func EditIssue(ctx context.TeaContext, index int64) (*task.EditIssueOption, error) {
	var opts = task.EditIssueOption{}
	var err error

	ctx.Owner, ctx.Repo, err = promptRepoSlug(ctx.Owner, ctx.Repo)
	if err != nil {
		return &opts, err
	}

	c := ctx.Login.Client()
	i, _, err := c.GetIssue(ctx.Owner, ctx.Repo, index)
	if err != nil {
		return &opts, err
	}

	opts = task.EditIssueOption{
		Index:    index,
		Title:    &i.Title,
		Body:     &i.Body,
		Deadline: i.Deadline,
	}

	if len(i.Assignees) != 0 {
		for _, a := range i.Assignees {
			opts.AddAssignees = append(opts.AddAssignees, a.UserName)
		}
	}

	if len(i.Labels) != 0 {
		for _, l := range i.Labels {
			opts.AddLabels = append(opts.AddLabels, l.Name)
		}
	}

	if i.Milestone != nil {
		opts.Milestone = &i.Milestone.Title
	}

	if err := promptIssueEditProperties(&ctx, &opts); err != nil {
		return &opts, err
	}

	return &opts, err
}

func promptIssueEditProperties(ctx *context.TeaContext, o *task.EditIssueOption) error {
	var milestoneName string
	var labelsSelected []string
	var err error

	selectableChan := make(chan (issueSelectables), 1)
	go fetchIssueSelectables(ctx.Login, ctx.Owner, ctx.Repo, selectableChan)

	// title
	promptOpts := survey.WithValidator(survey.Required)
	promptI := &survey.Input{Message: "Issue title:", Default: *o.Title}
	if err = survey.AskOne(promptI, o.Title, promptOpts); err != nil {
		return err
	}

	// description
	promptD := NewMultiline(Multiline{
		Message:             "Issue description:",
		Default:             *o.Body,
		Syntax:              "md",
		UseEditor:           config.GetPreferences().Editor,
		EditorAppendDefault: true,
		EditorHideDefault:   true,
	})

	if err = survey.AskOne(promptD, o.Body); err != nil {
		return err
	}

	// wait until selectables are fetched
	selectables := <-selectableChan
	if selectables.Err != nil {
		return selectables.Err
	}

	// skip remaining props if we don't have permission to set them
	if !selectables.Repo.Permissions.Push {
		return nil
	}

	currAssignees := o.AddAssignees
	newAssignees := selectables.Assignees

	for _, c := range currAssignees {
		if i := slices.Index(newAssignees, c); i != -1 {
			newAssignees = slices.Delete(newAssignees, i, i+1)
		}
	}

	// assignees
	if o.AddAssignees, err = promptMultiSelect("Add Assignees:", newAssignees, "[other]"); err != nil {
		return err
	}

	// milestone
	if len(selectables.MilestoneList) != 0 {
		var defaultMS string
		if o.Milestone != nil {
			defaultMS = *o.Milestone
		}
		if milestoneName, err = promptSelect("Milestone:", selectables.MilestoneList, "", "[none]", defaultMS); err != nil {
			return err
		}
		o.Milestone = &milestoneName
	}

	// labels
	if len(selectables.LabelList) != 0 {
		promptL := &survey.MultiSelect{Message: "Labels:", Options: selectables.LabelList, VimMode: true, Default: o.AddLabels}
		if err := survey.AskOne(promptL, &labelsSelected); err != nil {
			return err
		}
		// removed labels
		for _, l := range o.AddLabels {
			if !slices.Contains(labelsSelected, l) {
				o.RemoveLabels = append(o.RemoveLabels, l)
			}
		}
		// added labels
		o.AddLabels = make([]string, len(labelsSelected))
		for i, l := range labelsSelected {
			o.AddLabels[i] = l
		}
	}

	// deadline
	if o.Deadline, err = promptDatetime("Due date:"); err != nil {
		return err
	}

	return nil
}
