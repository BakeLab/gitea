// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMilestoneFormMarksTitleRequired(t *testing.T) {
	p, rendered := accessiblePrompter(t, "v1\n\n\n")
	_, err := p.EditMilestone(context.Background(), "", MilestoneFormValues{})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "Milestone title (required):")
}

func TestMilestoneFormStartsFromCurrentValues(t *testing.T) {
	due := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	p, _ := testPrompter(strings.NewReader("\r\r\r"))
	values, err := p.EditMilestone(context.Background(), "Editing milestone v1.0", MilestoneFormValues{Title: "v1.0", Description: "First", Deadline: &due})
	require.NoError(t, err)
	require.Equal(t, "v1.0", values.Title)
	require.Equal(t, "First", values.Description)
	require.Equal(t, due, *values.Deadline)
}
