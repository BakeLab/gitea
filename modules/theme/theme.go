// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package theme

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var giteaTheme = func() *huh.Theme {
	theme := huh.ThemeCharm()

	title := lipgloss.AdaptiveColor{Light: "#02BA84", Dark: "#02BF87"}
	theme.Focused.Title = theme.Focused.Title.Foreground(title).Bold(true)
	theme.Blurred = theme.Focused
	return theme
}()

// GetTheme returns the Gitea theme for Huh
func GetTheme() *huh.Theme {
	return giteaTheme
}
