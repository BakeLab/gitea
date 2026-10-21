// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
)

// ReviewCommentDraft is an inline comment collected during interactive review.
type ReviewCommentDraft struct {
	Path, Body       string
	OldLine, NewLine int64
}

// ReviewFormChoices describes the pull request under review and how its review is collected.
type ReviewFormChoices struct {
	// Header introduces the form, such as `Reviewing #1 "Add one.txt" (feat/one → main) in alice/x`.
	Header string
	// Author leaves out approving and requesting changes, which Gitea refuses on one's own pull request.
	Author bool
	// Body seeds the review body.
	Body string
	// Editor is the configured editor command that ctrl+e opens on the body, except in accessible mode; empty disables it.
	Editor string
	// ReviewDiff, when non-nil, offers to annotate the diff in the editor after the body.
	ReviewDiff func(context.Context) ([]ReviewCommentDraft, error)
	// Pending tells that the pending review a comment would publish has a body or inline
	// comments, so that the comment needs neither.
	Pending bool
}

// ReviewFormValues holds an interactively collected review: its API state, body and inline comments.
type ReviewFormValues struct {
	Event    string
	Body     string
	Comments []ReviewCommentDraft
}

// ReviewForm collects interactive pull review input.
type ReviewForm interface {
	CreateReview(context.Context, ReviewFormChoices) (ReviewFormValues, error)
}

// errEmptyComment rejects a comment review with neither a body nor inline comments.
var errEmptyComment = errors.New("a comment review needs a body or inline comments")

// CreateReview asks for the review type, then the body, then whether to annotate the diff, on stderr.
func (p *FormPrompter) CreateReview(ctx context.Context, choices ReviewFormChoices) (ReviewFormValues, error) {
	if choices.Header != "" {
		fmt.Fprintln(p.Streams.ErrOut, SafeLine(choices.Header))
	}
	options := []huh.Option[string]{huh.NewOption("Comment", "COMMENT")}
	if !choices.Author {
		options = append([]huh.Option[string]{huh.NewOption("Approve", "APPROVED"), huh.NewOption("Request changes", "REQUEST_CHANGES")}, options...)
	}
	values := ReviewFormValues{Event: options[0].Value, Body: choices.Body}
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Review type:").Options(options...).Value(&values.Event),
	))); err != nil {
		return values, err
	}

	// A comment can consist of inline comments alone, which are collected after the body, or of
	// the pending review it publishes.
	required := values.Event == "REQUEST_CHANGES" || (values.Event == "COMMENT" && choices.ReviewDiff == nil && !choices.Pending)
	title := "Body (markdown, optional):"
	if required {
		title = "Body (markdown, required):"
	}
	body := bodyField(title, &values.Body).withEditor(choices.Editor)
	if required {
		body.Validate(requiredText(&values.Body))
	}
	if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(body))); err != nil {
		return values, err
	}
	values.Body = strings.TrimSpace(values.Body)

	if choices.ReviewDiff != nil {
		annotate := false
		if err := p.runForm(ctx, huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title("Annotate the diff in your editor?").Value(&annotate),
		))); err != nil {
			return values, err
		}
		if annotate {
			comments, err := choices.ReviewDiff(ctx)
			if err != nil {
				return values, fmt.Errorf("diff review failed: %w", err)
			}
			values.Comments = comments
			fmt.Fprintf(p.Streams.ErrOut, "Found %s in the annotated diff\n", Plural(len(comments), "inline comment"))
		}
	}
	if values.Event == "COMMENT" && values.Body == "" && len(values.Comments) == 0 && !choices.Pending {
		return values, errEmptyComment
	}
	return values, nil
}
