// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestColorEnabledRespectsPreferenceAndEnvironment(t *testing.T) {
	getenv := func(key string) string {
		if key == "NO_COLOR" {
			return "1"
		}
		return ""
	}
	require.True(t, ColorEnabled("always", false, nil))
	require.False(t, ColorEnabled("never", true, nil))
	require.True(t, ColorEnabled("auto", true, func(string) string { return "" }))
	require.False(t, ColorEnabled("auto", false, func(string) string { return "" }))
	require.False(t, ColorEnabled("auto", true, getenv))
	require.True(t, ColorEnabled("always", true, getenv))
	require.False(t, ColorEnabled("auto", true, func(key string) string { return map[string]string{"TERM": "dumb"}[key] }))
}

func TestColorizeAndEmphasis(t *testing.T) {
	require.Equal(t, "bug", Colorize("ff0000", "bug", false))
	require.Equal(t, "bug", Colorize("bad", "bug", true))
	require.Equal(t, "\x1b[38;2;255;0;0mbug\x1b[0m", Colorize("#ff0000", "bug", true))
	require.Equal(t, "open", Emphasis("open", false))
	require.Equal(t, "\x1b[1mopen\x1b[0m", Emphasis("open", true))
}

func TestHyperlink(t *testing.T) {
	require.Equal(t, "#7", Hyperlink("https://example.org/i/7", "#7", false))
	require.Equal(t, "\x1b]8;;https://example.org/i/7\x1b\\#7\x1b]8;;\x1b\\", Hyperlink("https://example.org/i/7", "#7", true))
	require.Equal(t, "#7", Hyperlink("", "#7", true))
	require.Equal(t, "#7", Hyperlink("https://example.org/\x1b[2J", "#7", true))
	require.Equal(t, "#7", Hyperlink("https://example.org/\u009b2J", "#7", true))
}
