// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestParseRunTime(t *testing.T) {
	now := time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC)
	plus2 := time.FixedZone("UTC+2", 2*60*60)
	for _, tc := range []struct {
		input    string
		expected time.Time
	}{
		{"24h", now.Add(-24 * time.Hour)},
		{"0s", now},
		{"2028-01-01", time.Date(2028, 1, 1, 0, 0, 0, 0, plus2)},
		{"2028-01-01T00:30:00+02:00", time.Date(2027, 12, 31, 22, 30, 0, 0, time.UTC)},
		{"2028-01-01 08:00", time.Date(2028, 1, 1, 8, 0, 0, 0, plus2)},
	} {
		result, err := parseRunTime(tc.input, now.In(plus2))
		require.NoError(t, err)
		require.True(t, result.Equal(tc.expected), tc.input)
	}
	for _, input := range []string{"", "-24h", "not-a-date", "2028-02-30"} {
		_, err := parseRunTime(input, now)
		require.Error(t, err, input)
	}
}

func TestParseTimeRangeRejectsOnlyInvertedBounds(t *testing.T) {
	for _, tc := range []struct {
		since, until string
		wantErr      bool
	}{
		{"2026-10-01", "2026-10-02", false},
		{"2026-10-01", "2026-10-01", false},
		{"2026-10-02", "2026-10-01", true},
	} {
		cmd := &cobra.Command{}
		var bounds timeRange
		bounds.register(cmd, "since", "", "until", "")
		require.NoError(t, cmd.Flags().Parse([]string{"--since=" + tc.since, "--until=" + tc.until}))
		_, _, err := bounds.parse(time.Now())
		require.Equal(t, tc.wantErr, err != nil, tc.since+".."+tc.until)
	}
}
