// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package interact

import (
	"fmt"

	"code.gitea.io/tea/modules/theme"

	"github.com/charmbracelet/lipgloss"
)

// printTitleAndContent prints a title and content with the gitea theme
func printTitleAndContent(title, content string) {
	style := lipgloss.NewStyle().
		Foreground(theme.GetTheme().Blurred.Title.GetForeground()).Bold(true).
		Padding(0, 1)
	fmt.Print(style.Render(title), content+"\n")
}
