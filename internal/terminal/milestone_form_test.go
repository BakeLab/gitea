// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMilestoneFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "v1\n\n\n")
	_, err := p.CreateMilestone(context.Background())
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Milestone title (required):")
}
