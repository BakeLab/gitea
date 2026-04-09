// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/require"
)

func TestActionRunsListEmpty(t *testing.T) {
	// Test with empty runs - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionRunsList panicked with empty list: %v", r)
		}
	}()

	require.NoError(t, ActionRunsList([]*gitea.ActionWorkflowRun{}, ""))
}

func TestActionRunsListWithData(t *testing.T) {
	runs := []*gitea.ActionWorkflowRun{
		{
			ID:           1,
			Status:       "success",
			DisplayTitle: "Test Workflow",
			HeadBranch:   "main",
			Event:        "push",
			StartedAt:    time.Now().Add(-1 * time.Hour),
			CompletedAt:  time.Now().Add(-30 * time.Minute),
		},
		{
			ID:         2,
			Status:     "in_progress",
			Path:       ".gitea/workflows/test.yml",
			HeadBranch: "feature",
			Event:      "pull_request",
			StartedAt:  time.Now().Add(-10 * time.Minute),
		},
	}

	// Test that it doesn't panic with real data
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionRunsList panicked with data: %v", r)
		}
	}()

	require.NoError(t, ActionRunsList(runs, ""))
}

func TestActionRunDetails(t *testing.T) {
	run := &gitea.ActionWorkflowRun{
		ID:           123,
		RunNumber:    42,
		Status:       "success",
		Conclusion:   "success",
		DisplayTitle: "Build and Test",
		Path:         ".gitea/workflows/ci.yml",
		HeadBranch:   "main",
		Event:        "push",
		HeadSha:      "abc123def456",
		StartedAt:    time.Now().Add(-2 * time.Hour),
		CompletedAt:  time.Now().Add(-1 * time.Hour),
		RunAttempt:   1,
		Actor: &gitea.User{
			UserName: "testuser",
		},
		HTMLURL: "https://gitea.example.com/owner/repo/actions/runs/123",
	}

	// Test that it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionRunDetails panicked: %v", r)
		}
	}()

	ActionRunDetails(run)
}

func TestActionWorkflowJobsListEmpty(t *testing.T) {
	// Test with empty jobs - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowJobsList panicked with empty list: %v", r)
		}
	}()

	require.NoError(t, ActionWorkflowJobsList([]*gitea.ActionWorkflowJob{}, ""))
}

func TestActionWorkflowJobsListWithData(t *testing.T) {
	jobs := []*gitea.ActionWorkflowJob{
		{
			ID:          1,
			Name:        "build",
			Status:      "success",
			RunnerName:  "runner-1",
			StartedAt:   time.Now().Add(-30 * time.Minute),
			CompletedAt: time.Now().Add(-20 * time.Minute),
		},
		{
			ID:         2,
			Name:       "test",
			Status:     "in_progress",
			RunnerName: "runner-2",
			StartedAt:  time.Now().Add(-5 * time.Minute),
		},
	}

	// Test that it doesn't panic with real data
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowJobsList panicked with data: %v", r)
		}
	}()

	require.NoError(t, ActionWorkflowJobsList(jobs, ""))
}

func TestActionWorkflowsListEmpty(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowsList panicked with empty list: %v", r)
		}
	}()

	require.NoError(t, ActionWorkflowsList([]*gitea.ActionWorkflow{}, ""))
}

func TestActionWorkflowsListWithData(t *testing.T) {
	workflows := []*gitea.ActionWorkflow{
		{
			ID:    "1",
			Name:  "CI",
			Path:  ".gitea/workflows/ci.yml",
			State: "active",
		},
		{
			ID:    "2",
			Name:  "Deploy",
			Path:  ".gitea/workflows/deploy.yml",
			State: "disabled_manually",
		},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowsList panicked with data: %v", r)
		}
	}()

	require.NoError(t, ActionWorkflowsList(workflows, ""))
}

func TestActionWorkflowDetails(t *testing.T) {
	wf := &gitea.ActionWorkflow{
		ID:        "1",
		Name:      "CI Pipeline",
		Path:      ".gitea/workflows/ci.yml",
		State:     "active",
		HTMLURL:   "https://gitea.example.com/owner/repo/actions/workflows/ci.yml",
		BadgeURL:  "https://gitea.example.com/owner/repo/actions/workflows/ci.yml/badge.svg",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowDetails panicked: %v", r)
		}
	}()

	ActionWorkflowDetails(wf)
}

func TestActionWorkflowDispatchResult(t *testing.T) {
	details := &gitea.RunDetails{
		WorkflowRunID: 42,
		HTMLURL:       "https://gitea.example.com/owner/repo/actions/runs/42",
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowDispatchResult panicked: %v", r)
		}
	}()

	ActionWorkflowDispatchResult(details)
}

func TestActionWorkflowDispatchResultNil(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ActionWorkflowDispatchResult panicked with nil: %v", r)
		}
	}()

	ActionWorkflowDispatchResult(nil)
}

func TestFormatDurationMinutes(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		started   time.Time
		completed time.Time
		expected  string
	}{
		{
			name:      "zero started",
			started:   time.Time{},
			completed: now,
			expected:  "",
		},
		{
			name:      "30 seconds",
			started:   now.Add(-30 * time.Second),
			completed: now,
			expected:  "30s",
		},
		{
			name:      "5 minutes",
			started:   now.Add(-5 * time.Minute),
			completed: now,
			expected:  "5m",
		},
		{
			name:      "in progress (no completed)",
			started:   now.Add(-1 * time.Hour),
			completed: time.Time{},
			expected:  "1h0m",
		},
		{
			name:      "2 hours 30 minutes",
			started:   now.Add(-150 * time.Minute),
			completed: now,
			expected:  "2h30m",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := formatDurationMinutes(test.started, test.completed)
			if result != test.expected {
				t.Errorf("formatDurationMinutes() = %q, want %q", result, test.expected)
			}
		})
	}
}
