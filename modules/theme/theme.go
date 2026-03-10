// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package theme

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

type myTheme struct{}

func (t myTheme) Theme(isDark bool) *huh.Styles {
	theme := huh.ThemeCharm(isDark)

	title := compat.AdaptiveColor{Light: lipgloss.Color("#02BA84"), Dark: lipgloss.Color("#02BF87")}
	theme.Focused.Title = theme.Focused.Title.Foreground(title).Bold(true)
	theme.Blurred = theme.Focused
	return theme
}

func GetTheme() myTheme {
	var t myTheme
	return t
}
