// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"fmt"
	"strings"

	"charm.land/huh/v2"
)

// ReviewCommentDraft is an inline comment collected during interactive review.
type ReviewCommentDraft struct {
	Path, Body       string
	OldLine, NewLine int64
}

// ReviewFormChoices supplies callbacks for interactive pull review.
type ReviewFormChoices struct {
	// ReviewDiff, when non-nil, runs after the user opts into annotating the diff.
	ReviewDiff func(context.Context) ([]ReviewCommentDraft, error)
	EditBody   BodyEditor
}

// ReviewFormValues holds interactively collected review fields.
type ReviewFormValues struct {
	Event    string
	Body     string
	Comments []ReviewCommentDraft
	Draft    Draft
}

// ReviewForm collects interactive pull review input.
type ReviewForm interface {
	CreateReview(context.Context, ReviewFormChoices) (ReviewFormValues, error)
}

// CreateReview runs the interactive review form on stderr.
func (p *FormPrompter) CreateReview(ctx context.Context, choices ReviewFormChoices) (ReviewFormValues, error) {
	var comments []ReviewCommentDraft
	if choices.ReviewDiff != nil {
		reviewDiff := true
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title("Review / comment the diff?").Value(&reviewDiff),
		))); err != nil {
			return ReviewFormValues{}, err
		}
		if reviewDiff {
			parsed, err := choices.ReviewDiff(ctx)
			if err != nil {
				return ReviewFormValues{}, fmt.Errorf("diff review failed: %w", err)
			}
			comments = append(comments, parsed...)
			fmt.Fprintf(p.Streams.ErrOut, "Found %d code comments in your review\n", len(comments))
		}
	}

	event := "COMMENT"
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Your assessment:").Options(
			huh.NewOption("comment", "COMMENT"),
			huh.NewOption("request changes", "REQUEST_CHANGES"),
			huh.NewOption("approve", "APPROVED"),
		).Value(&event),
	))); err != nil {
		return ReviewFormValues{}, err
	}

	body := ""
	values := ReviewFormValues{Event: event, Comments: comments}
	required := (event == "COMMENT" && len(comments) == 0) || event == "REQUEST_CHANGES"
	if choices.EditBody != nil {
		var err error
		if body, values.Draft, err = editBody(ctx, choices.EditBody, ""); err != nil {
			return values, err
		}
	} else {
		field := huh.NewText().Title("Concluding comment(markdown):").Value(&body)
		if required {
			field = field.Title("Concluding comment(markdown) (required):").Validate(huh.ValidateNotEmpty())
		}
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(field))); err != nil {
			return ReviewFormValues{}, err
		}
	}
	body = strings.TrimSpace(body)
	if required && body == "" {
		return values, fmt.Errorf("concluding comment cannot be empty")
	}
	values.Body = body
	return values, nil
}
