// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package theme

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// TeaTheme implements the huh.Theme interface with tea-cli styling.
type TeaTheme struct{}

// Theme implements the huh.Theme interface.
func (t TeaTheme) Theme(isDark bool) *huh.Styles {
	theme := huh.ThemeCharm(isDark)

	title := compat.AdaptiveColor{Light: lipgloss.Color("#02BA84"), Dark: lipgloss.Color("#02BF87")}
	theme.Focused.Title = theme.Focused.Title.Foreground(title).Bold(true)
	theme.Blurred = theme.Focused
	return theme
}

// GetTheme returns the default theme for huh prompts.
func GetTheme() TeaTheme {
	var t TeaTheme
	return t
}
