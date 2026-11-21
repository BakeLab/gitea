// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"math"
	"strconv"
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
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
		{"0s", now},
		{"2028-01-01", time.Date(2028, 1, 1, 0, 0, 0, 0, plus2)},
		{"2028-01-01T00:30:00+02:00", time.Date(2027, 12, 31, 22, 30, 0, 0, time.UTC)},
		{"2028-01-01 08:00", time.Date(2028, 1, 1, 8, 0, 0, 0, plus2)},
	} {
		result, err := parseRunTime(tc.input, now.In(plus2))
		require.NoError(t, err)
		require.True(t, result.Equal(tc.expected), tc.input)
	}
	for _, input := range []string{"", "-24h", "not-a-date", "2028-02-30", "-1d", "1.5w"} {
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
		bounds.register(cmd, "", "")
		require.NoError(t, cmd.Flags().Parse([]string{"--since=" + tc.since, "--until=" + tc.until}))
		_, _, err := bounds.parse(time.Now())
		require.Equal(t, tc.wantErr, err != nil, tc.since+".."+tc.until)
	}
}

func TestNumberAcceptsHashPrefix(t *testing.T) {
	for raw, want := range map[string]int64{"12": 12, "#12": 12} {
		n, err := number(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, n, raw)
	}
	for _, raw := range []string{"#", "##1", "0", "#-1", "1#", "abc"} {
		_, err := number(raw)
		require.ErrorContains(t, err, "invalid resource number", raw)
	}
}

func TestUsageArgumentsSplitTopLevelGroups(t *testing.T) {
	for use, want := range map[string][]string{
		"list":                               nil,
		"list [flags]":                       nil,
		"view <number>":                      {"<number>"},
		"view [number]":                      {"[number]"},
		"close <number>...":                  {"<number>..."},
		"clone <repository> [directory]":     {"<repository>", "[directory]"},
		"diff [<number> | <url> | <branch>]": {"[<number> | <url> | <branch>]"},
		"download <tag> <asset-id> [flags]":  {"<tag>", "<asset-id>"},
		"upload <tag> <file>... [flags]":     {"<tag>", "<file>..."},
	} {
		require.Equal(t, want, usageArguments(use), use)
	}
}

func TestArgumentErrorsShowTheUsageLine(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "view"}, "missing issue number\nUsage: gt issue view {<number> | <url>} [flags]\nRun 'gt issue view --help' for examples."},
		{[]string{"issue", "view", "1", "2"}, "too many arguments\nUsage: gt issue view {<number> | <url>} [flags]"},
		{[]string{"issue", "list", "extra"}, "unexpected argument \"extra\"\nUsage: gt issue list [flags]"},
		{[]string{"pr", "checkout"}, "missing pull request number\nUsage: gt pr checkout {<number> | <url> | <branch>} [flags]"},
		{[]string{"run", "view"}, "missing run ID\nUsage: gt run view [<run-id>] [flags]"},
		{[]string{"release", "asset", "edit", "v1"}, "missing asset\nUsage: gt release asset edit <tag> <asset> [flags]"},
		{[]string{"time", "create"}, "missing issue number\nUsage: gt time create {<number> | <url>} <duration> [flags]"},
		{[]string{"milestone", "issue", "add", "v1.0"}, "missing issue number\nUsage: gt milestone issue add <milestone> {<number> | <url>}... [flags]"},
		{[]string{"branch", "protect", "main", "--pattern", "release/*"}, "--pattern cannot be combined with branch names\nUsage: gt branch protect <branch>... [flags]"},
		{[]string{"repo", "clone"}, "missing repository\nUsage: gt repo clone <repository> [<directory>] [-- <gitflags>...] [flags]"},
	} {
		requireUsageBeforeRequests(t, tc.want, tc.args...)
	}
}

func TestLimitAcceptsShorthandUpToOneThousand(t *testing.T) {
	var limit, page int
	cmd := &cobra.Command{Use: "list"}
	pageFlags(cmd, &limit, &page)
	require.Equal(t, 30, limit)
	require.NoError(t, cmd.Flags().Parse([]string{"-L", "1000"}))
	require.Equal(t, 1000, limit)
	require.ErrorContains(t, cmd.Flags().Parse([]string{"-L", "1001"}), "must be 1..1000")
}

func TestPageRejectsValuesThatOverflowTheOffset(t *testing.T) {
	var limit, page int
	cmd := &cobra.Command{Use: "list"}
	pageFlags(cmd, &limit, &page)
	require.NoError(t, cmd.Flags().Parse([]string{"--page", strconv.Itoa(maxPage)}))
	require.Equal(t, maxPage, page)
	require.ErrorContains(t, cmd.Flags().Parse([]string{"--page", strconv.Itoa(math.MaxInt)}), "--page")
}
