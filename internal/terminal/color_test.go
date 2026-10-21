// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestColorEnabledRespectsPreferenceAndEnvironment(t *testing.T) {
	noColor := env(map[string]string{"NO_COLOR": "1"})
	require.True(t, ColorEnabled("always", false, nil))
	require.False(t, ColorEnabled("never", true, nil))
	require.True(t, ColorEnabled("auto", true, env(nil)))
	require.False(t, ColorEnabled("auto", false, env(nil)))
	require.False(t, ColorEnabled("auto", true, noColor))
	require.True(t, ColorEnabled("always", true, noColor))
	require.False(t, ColorEnabled("auto", true, env(map[string]string{"TERM": "dumb"})))
}

func TestColorEnabledHonorsCLICOLOR(t *testing.T) {
	for _, tc := range []struct {
		preference string
		tty        bool
		env        map[string]string
		want       bool
	}{
		{"auto", false, map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"", false, map[string]string{"CLICOLOR_FORCE": "yes"}, true},
		{"auto", true, map[string]string{"CLICOLOR_FORCE": "1", "TERM": "dumb"}, true},
		{"auto", false, map[string]string{"CLICOLOR_FORCE": "0"}, false},
		{"auto", false, map[string]string{"CLICOLOR_FORCE": ""}, false},
		{"auto", true, map[string]string{"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}, false},
		{"auto", true, map[string]string{"CLICOLOR": "0"}, false},
		{"auto", true, map[string]string{"CLICOLOR": "1"}, true},
		{"auto", false, map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"}, true},
		{"never", true, map[string]string{"CLICOLOR_FORCE": "1"}, false},
		{"always", false, map[string]string{"CLICOLOR": "0"}, true},
	} {
		require.Equal(t, tc.want, ColorEnabled(tc.preference, tc.tty, env(tc.env)), "%+v", tc)
	}
}

func TestHyperlinksEnabledIgnoresColorPolicy(t *testing.T) {
	for _, tc := range []struct {
		preference string
		tty        bool
		env        map[string]string
		want       bool
	}{
		{"auto", true, nil, true},
		{"", true, map[string]string{"NO_COLOR": "1"}, true},
		{"auto", false, nil, false},
		{"auto", true, map[string]string{"TERM": "dumb"}, false},
		{"always", false, map[string]string{"TERM": "dumb"}, true},
		{"never", true, nil, false},
	} {
		require.Equal(t, tc.want, HyperlinksEnabled(tc.preference, tc.tty, env(tc.env)), "%+v", tc)
	}
}

func TestColorizeAndEmphasis(t *testing.T) {
	require.Equal(t, "bug", Colorize("ff0000", "bug", false))
	require.Equal(t, "bug", Colorize("bad", "bug", true))
	require.Equal(t, "bug", Colorize("#ff0000", "bug", true))
	require.Equal(t, "\x1b[32mopen\x1b[0m", Colorize("green", "open", true))
	require.Equal(t, "\x1b[90mdraft\x1b[0m", Colorize("muted", "draft", true))
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
