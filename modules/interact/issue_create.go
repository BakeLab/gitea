// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"strings"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/theme"

	"github.com/charmbracelet/huh"
)

// IsQuitting checks if the user has aborted the interactive prompt
func IsQuitting(err error) bool {
	return err == huh.ErrUserAborted
}

// CreateIssue interactively creates an issue
func CreateIssue(login *config.Login, owner, repo string) error {
	owner, repo, err := promptRepoSlug(owner, repo)
	if err != nil {
		return err
	}
	printTitleAndContent("Target repo:", owner+"/"+repo)

	var opts gitea.CreateIssueOption
	if err := promptIssueProperties(login, owner, repo, &opts); err != nil {
		return err
	}

	return task.CreateIssue(login, owner, repo, opts)
}

func promptIssueProperties(login *config.Login, owner, repo string, o *gitea.CreateIssueOption) error {
	var milestoneName string
	var err error

	selectableChan := make(chan (issueSelectables), 1)
	go fetchIssueSelectables(login, owner, repo, selectableChan)

	// title
	if err := huh.NewInput().
		Title("Issue title:").
		Value(&o.Title).
		Validate(huh.ValidateNotEmpty()).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("Issue title:", o.Title)

	// description
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("Issue description(markdown):").
				ExternalEditor(config.GetPreferences().Editor).
				EditorExtension("md").
				Value(&o.Body),
		),
	).WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("Issue description(markdown):", o.Body)

	// wait until selectables are fetched
	selectables := <-selectableChan
	if selectables.Err != nil {
		return selectables.Err
	}

	// skip remaining props if we don't have permission to set them
	if !selectables.Repo.Permissions.Push {
		return nil
	}

	// assignees
	if o.Assignees, err = promptMultiSelect("Assignees:", selectables.Assignees, "[other]"); err != nil {
		return err
	}
	printTitleAndContent("Assignees:", strings.Join(o.Assignees, "\n"))

	// milestone
	if len(selectables.MilestoneList) != 0 {
		if milestoneName, err = promptSelect("Milestone:", selectables.MilestoneList, "", "[none]", ""); err != nil {
			return err
		}
		o.Milestone = selectables.MilestoneMap[milestoneName]
		printTitleAndContent("Milestone:", milestoneName)
	}

	// labels
	if len(selectables.LabelList) != 0 {
		options := make([]huh.Option[int64], 0, len(selectables.LabelList))
		labelsMap := make(map[int64]string, len(selectables.LabelList))
		for _, l := range selectables.LabelList {
			options = append(options, huh.Option[int64]{Key: l, Value: selectables.LabelMap[l]})
			labelsMap[selectables.LabelMap[l]] = l
		}
		if err := huh.NewMultiSelect[int64]().
			Title("Labels:").
			Options(options...).
			Value(&o.Labels).
			Run(); err != nil {
			return err
		}
		var labels []string
		for _, labelID := range o.Labels {
			labels = append(labels, labelsMap[labelID])
		}
		printTitleAndContent("Labels:", strings.Join(labels, "\n"))
	}

	// deadline
	if o.Deadline, err = promptDatetime("Due date:"); err != nil {
		return err
	}
	deadlineStr := "No due date"
	if o.Deadline != nil && !o.Deadline.IsZero() {
		deadlineStr = o.Deadline.Format("2006-01-02")
	}
	printTitleAndContent("Due date:", deadlineStr)

	return nil
}

type issueSelectables struct {
	Repo          *gitea.Repository
	Assignees     []string
	MilestoneList []string
	MilestoneMap  map[string]int64
	LabelList     []string
	LabelMap      map[string]int64
	Err           error
}

func fetchIssueSelectables(login *config.Login, owner, repo string, done chan issueSelectables) {
	// TODO PERF make these calls concurrent
	r := issueSelectables{}
	c := login.Client()

	r.Repo, _, r.Err = c.GetRepo(owner, repo)
	if r.Err != nil {
		done <- r
		return
	}
	// we can set the following properties only if we have write access to the repo
	// so we fastpath this if not.
	if !r.Repo.Permissions.Push {
		done <- r
		return
	}

	assignees, _, err := c.GetAssignees(owner, repo)
	if err != nil {
		r.Err = err
		done <- r
		return
	}
	r.Assignees = make([]string, len(assignees))
	for i, u := range assignees {
		r.Assignees[i] = u.UserName
	}

	milestones, _, err := c.ListRepoMilestones(owner, repo, gitea.ListMilestoneOption{})
	if err != nil {
		r.Err = err
		done <- r
		return
	}
	r.MilestoneMap = make(map[string]int64)
	r.MilestoneList = make([]string, len(milestones))
	for i, m := range milestones {
		r.MilestoneMap[m.Title] = m.ID
		r.MilestoneList[i] = m.Title
	}

	labels, _, err := c.ListRepoLabels(owner, repo, gitea.ListLabelsOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		r.Err = err
		done <- r
		return
	}
	r.LabelMap = make(map[string]int64)
	r.LabelList = make([]string, len(labels))
	for i, l := range labels {
		r.LabelMap[l.Name] = l.ID
		r.LabelList[i] = l.Name
	}

	done <- r
}
