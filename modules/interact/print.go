// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"

	"gitea.dev/tea/modules/theme"

	"charm.land/lipgloss/v2"
)

// printTitleAndContent prints a title and content with the gitea theme
func printTitleAndContent(title, content string) {
	hasDarkBG := theme.HasDarkBackground()
	style := lipgloss.NewStyle().
		Foreground(theme.GetTheme().Theme(hasDarkBG).Blurred.Title.GetForeground()).Bold(true).
		Padding(0, 1)
	fmt.Print(style.Render(title), content+"\n")
}
