// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

// linePrompter is accessiblePrompter reading its input a byte at a time, so that every field of
// consecutive forms gets its own line.
func linePrompter(t *testing.T, input string) (*FormPrompter, *strings.Builder) {
	t.Helper()
	t.Setenv("TERM", "dumb")
	p, _ := testPrompter(iotest.OneByteReader(strings.NewReader(input)))
	rendered := &strings.Builder{}
	p.Streams.ErrOut = rendered
	return p, rendered
}

func TestCreateReviewAbortsWhenDiffReviewFails(t *testing.T) {
	failure := errors.New("editor failed")
	p, _ := linePrompter(t, "1\nLooks good\n.\ny\n")
	_, err := p.CreateReview(context.Background(), ReviewFormChoices{
		ReviewDiff: func(context.Context) ([]ReviewCommentDraft, error) { return nil, failure },
	})
	require.ErrorIs(t, err, failure)
}

func TestReviewFormAsksTypeThenBody(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		choices     ReviewFormChoices
		want        ReviewFormValues
		title, err  string
		hidden      []string
	}{
		{name: "approve without body", input: "1\n\n", want: ReviewFormValues{Event: "APPROVED"}, title: "Body (markdown, optional):"},
		{name: "request changes", input: "2\nPlease add tests\n.\n", want: ReviewFormValues{Event: "REQUEST_CHANGES", Body: "Please add tests"}, title: "Body (markdown, required):"},
		{name: "comment needs a body", input: "3\n\n\n", err: "canceled", title: "input cannot be empty"},
		{name: "comment publishing a pending review", input: "3\n\n", choices: ReviewFormChoices{Pending: true}, want: ReviewFormValues{Event: "COMMENT"}, title: "Body (markdown, optional):"},
		{name: "author can only comment", input: "1\nSelf note\n.\n", choices: ReviewFormChoices{Author: true}, want: ReviewFormValues{Event: "COMMENT", Body: "Self note"}, hidden: []string{"Approve", "Request changes"}},
		// No key opens an editor in accessible mode.
		{name: "no editor shortcut", input: "1\n\n", choices: ReviewFormChoices{Editor: "vi"}, want: ReviewFormValues{Event: "APPROVED"}, title: "Body (markdown, optional):", hidden: []string{"ctrl+e"}},
		{name: "header", input: "1\n\n", choices: ReviewFormChoices{Header: "Reviewing #1 \"Add\x1b[31m one\" (feat/one → main) in alice/x"}, want: ReviewFormValues{Event: "APPROVED"}, title: "Reviewing #1 \"Add[31m one\" (feat/one → main) in alice/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, rendered := linePrompter(t, tc.input)
			values, err := p.CreateReview(context.Background(), tc.choices)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, values)
			}
			require.Contains(t, rendered.String(), tc.title)
			for _, hidden := range tc.hidden {
				require.NotContains(t, rendered.String(), hidden)
			}
		})
	}
}

func TestReviewFormCommentMayBeInlineOnly(t *testing.T) {
	p, rendered := linePrompter(t, "3\n\ny\n")
	values, err := p.CreateReview(context.Background(), ReviewFormChoices{
		ReviewDiff: func(context.Context) ([]ReviewCommentDraft, error) {
			return []ReviewCommentDraft{{Path: "one.txt", Body: "nit", NewLine: 1}}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, ReviewFormValues{Event: "COMMENT", Comments: []ReviewCommentDraft{{Path: "one.txt", Body: "nit", NewLine: 1}}}, values)
	require.Contains(t, rendered.String(), "Found 1 inline comment in the annotated diff\n")
}
