// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateReviewAbortsWhenDiffReviewFails(t *testing.T) {
	failure := errors.New("editor failed")
	p, _ := accessiblePrompter(t, "y\n1\n")
	_, err := p.CreateReview(context.Background(), ReviewFormChoices{
		ReviewDiff: func(context.Context) ([]ReviewCommentDraft, error) { return nil, failure },
	})
	require.ErrorIs(t, err, failure)
}

func TestReviewFormRequiresCommentOnlyWhenRequestingChanges(t *testing.T) {
	p, rendered := accessiblePrompter(t, "3\n")
	_, err := p.CreateReview(context.Background(), ReviewFormChoices{})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Concluding comment(markdown):")
	require.NotContains(t, rendered.String(), "(required)")

	p, rendered = accessiblePrompter(t, "2\n")
	_, err = p.CreateReview(context.Background(), ReviewFormChoices{})
	require.ErrorContains(t, err, "concluding comment cannot be empty")
	require.Contains(t, rendered.String(), "Concluding comment(markdown) (required):")
}
