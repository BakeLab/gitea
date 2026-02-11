// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package runs

import (
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
)

func TestFilterRunsByTime(t *testing.T) {
	now := time.Now()
	runs := []*gitea.ActionWorkflowRun{
		{ID: 1, StartedAt: now.Add(-1 * time.Hour)},
		{ID: 2, StartedAt: now.Add(-2 * time.Hour)},
		{ID: 3, StartedAt: now.Add(-3 * time.Hour)},
		{ID: 4, StartedAt: now.Add(-4 * time.Hour)},
		{ID: 5, StartedAt: now.Add(-5 * time.Hour)},
	}

	tests := []struct {
		name     string
		since    time.Time
		until    time.Time
		expected []int64
	}{
		{
			name:     "no filter",
			since:    time.Time{},
			until:    time.Time{},
			expected: []int64{1, 2, 3, 4, 5},
		},
		{
			name:     "since 2.5 hours ago",
			since:    now.Add(-150 * time.Minute),
			until:    time.Time{},
			expected: []int64{1, 2},
		},
		{
			name:     "until 2.5 hours ago",
			since:    time.Time{},
			until:    now.Add(-150 * time.Minute),
			expected: []int64{3, 4, 5},
		},
		{
			name:     "between 2 and 4 hours ago",
			since:    now.Add(-4 * time.Hour),
			until:    now.Add(-2 * time.Hour),
			expected: []int64{2, 3, 4},
		},
		{
			name:     "filter excludes all",
			since:    now.Add(-30 * time.Minute),
			until:    time.Time{},
			expected: []int64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterRunsByTime(runs, tt.since, tt.until)

			if len(result) != len(tt.expected) {
				t.Errorf("filterRunsByTime() returned %d runs, want %d", len(result), len(tt.expected))
				return
			}

			for i, run := range result {
				if run.ID != tt.expected[i] {
					t.Errorf("filterRunsByTime()[%d].ID = %d, want %d", i, run.ID, tt.expected[i])
				}
			}
		})
	}
}
