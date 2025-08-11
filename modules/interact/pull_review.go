// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"
	"os"
	"strconv"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/theme"

	"code.gitea.io/sdk/gitea"
	"github.com/charmbracelet/huh"
)

var reviewStates = map[string]gitea.ReviewStateType{
	"approve":         gitea.ReviewStateApproved,
	"comment":         gitea.ReviewStateComment,
	"request changes": gitea.ReviewStateRequestChanges,
}
var reviewStateOptions = []string{"comment", "request changes", "approve"}

// ReviewPull interactively reviews a PR
func ReviewPull(ctx *context.TeaContext, idx int64) error {
	var state gitea.ReviewStateType
	var comment string
	var codeComments []gitea.CreatePullReviewComment
	var err error

	// codeComments
	reviewDiff := true
	if err := huh.NewConfirm().
		Title("Review / comment the diff?").
		Value(&reviewDiff).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("Review / comment the diff?", strconv.FormatBool(reviewDiff))

	if reviewDiff {
		if codeComments, err = DoDiffReview(ctx, idx); err != nil {
			fmt.Printf("Error during diff review: %s\n", err)
		}
		fmt.Printf("Found %d code comments in your review\n", len(codeComments))
	}

	// state
	var stateString string
	if err := huh.NewSelect[string]().
		Title("Your assessment:").
		Options(huh.NewOptions(reviewStateOptions...)...).
		Value(&stateString).
		WithTheme(theme.GetTheme()).
		Run(); err != nil {
		return err
	}
	printTitleAndContent("Your assessment:", stateString)

	state = reviewStates[stateString]

	// comment
	field := huh.NewText().
		Title("Concluding comment(markdown):").
		ExternalEditor(config.GetPreferences().Editor).
		EditorExtension("md").
		Value(&comment)
	if (state == gitea.ReviewStateComment && len(codeComments) == 0) || state == gitea.ReviewStateRequestChanges {
		field = field.Validate(huh.ValidateNotEmpty())
	}
	if err := huh.NewForm(huh.NewGroup(field)).WithTheme(theme.GetTheme()).Run(); err != nil {
		return err
	}
	printTitleAndContent("Concluding comment(markdown):", comment)

	return task.CreatePullReview(ctx, idx, state, comment, codeComments)
}

// DoDiffReview (1) fetches & saves diff in tempfile, (2) starts $VISUAL or $EDITOR to comment on diff,
// (3) parses resulting file into code comments.
// It doesn't really make sense to use survey.Editor() here, as we'd read the file content at least twice.
func DoDiffReview(ctx *context.TeaContext, idx int64) ([]gitea.CreatePullReviewComment, error) {
	tmpFile, err := task.SavePullDiff(ctx, idx)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile)

	if err = task.OpenFileInEditor(tmpFile); err != nil {
		return nil, err
	}

	return task.ParseDiffComments(tmpFile)
}
