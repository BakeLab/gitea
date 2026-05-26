// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	stdctx "context"
	"fmt"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/theme"

	"charm.land/huh/v2"
)

// CreateMilestone interactively creates a milestone
func CreateMilestone(requestCtx stdctx.Context, login *config.Login, owner, repo string) error {
	var title, description, deadline string

	// owner, repo
	owner, repo, err := promptRepoSlug(owner, repo)
	if err != nil {
		return err
	}
	printTitleAndContent("Target repo:", fmt.Sprintf("%s/%s", owner, repo))

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Milestone title:").
				Validate(huh.ValidateNotEmpty()).
				Value(&title),
			huh.NewText().
				Title("Milestone description(markdown):").
				ExternalEditor(config.GetPreferences().Editor).
				EditorExtension("md").
				Value(&description),
			huh.NewInput().
				Title("Milestone deadline:").
				Placeholder("YYYY-MM-DD").
				Validate(func(s string) error {
					if s == "" {
						return nil // no deadline
					}
					_, err := time.Parse("2006-01-02", s)
					return err
				}).
				Value(&deadline),
		),
	).WithTheme(theme.GetTheme()).Run(); err != nil {
		return err
	}

	var deadlineTM *time.Time
	if deadline != "" {
		tm, _ := time.Parse("2006-01-02", deadline)
		deadlineTM = &tm
	}

	return task.CreateMilestone(requestCtx, login,
		owner,
		repo,
		title,
		description,
		deadlineTM,
		gitea.StateOpen)
}
