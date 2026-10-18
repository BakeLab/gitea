// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "less than a minute ago"},
		{now.Add(-time.Minute), "about 1 minute ago"},
		{now.Add(-3 * time.Hour), "about 3 hours ago"},
		{now.AddDate(0, -2, 0), "about 2 months ago"},
		{now.AddDate(-2, -1, 0), "about 2 years ago"},
		{now.Add(10 * time.Second), "in less than a minute"},
		{now.AddDate(0, 0, 3), "in about 3 days"},
	} {
		require.Equal(t, tc.want, RelativeTime(now, tc.at), tc.at)
	}
}

func TestTimestamp(t *testing.T) {
	at := time.Date(2026, 10, 11, 20, 48, 23, 588823137, time.FixedZone("CEST", 2*3600))
	require.Equal(t, "2026-10-11T18:48:23Z", Timestamp(at))
	require.Equal(t, at.Unix(), ParseTime("2026-10-11T20:48:23+02:00").Unix())
	require.True(t, ParseTime("").IsZero())
	require.True(t, ParseTime("yesterday").IsZero())
}

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 13: "13 B", 1023: "1023 B", 1536: "1.5 KiB", 3040870: "2.9 MiB", 5 << 30: "5.0 GiB", 3 << 50: "3072.0 TiB"} {
		require.Equal(t, want, Bytes(n), n)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "0s", 45 * time.Second: "45s", 72 * time.Second: "1m 12s", 90 * time.Minute: "1h 30m", 2 * time.Hour: "2h 0m", 1499 * time.Millisecond: "1s"} {
		require.Equal(t, want, Duration(d), d)
	}
}

func TestStatusIcon(t *testing.T) {
	for _, tc := range []struct {
		status, conclusion string
		want               Cell
	}{
		{"completed", "success", Cell{Text: "✓", Color: "green"}},
		{"success", "", Cell{Text: "✓", Color: "green"}},
		{"completed", "failure", Cell{Text: "X", Color: "red"}},
		{"error", "", Cell{Text: "X", Color: "red"}},
		{"warning", "", Cell{Text: "X", Color: "red"}},
		{"completed", "cancelled", Cell{Text: "-", Muted: true}}, //nolint:misspell // Gitea API spelling.
		{"skipped", "", Cell{Text: "-", Muted: true}},
		{"queued", "", Cell{Text: "*", Color: "yellow"}},
		{"pending", "", Cell{Text: "*", Color: "yellow"}},
	} {
		require.Equal(t, tc.want, StatusIcon(tc.status, tc.conclusion), "%s/%s", tc.status, tc.conclusion)
	}
}
