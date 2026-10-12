// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
)

// MarkdownEnabled resolves the markdown preference: raw never renders, and auto renders on TTY stdout.
func MarkdownEnabled(preference string, outTTY bool) bool {
	return outTTY && preference != "raw"
}

// glamourLinks matches the OSC 8 hyperlinks that glamour writes for markdown links.
var glamourLinks = regexp.MustCompile("\x1b\\]8;[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// Markdown renders sanitized markdown for d: wrapped to its width (80 when unknown), colored or plain,
// with links only when d allows them. It keeps line breaks like Gitea does, and returns the sanitized
// text unrendered if rendering fails.
func Markdown(text string, d Display) string {
	text = SafeText(text)
	width := d.Width
	if width <= 0 {
		width = 80
	}
	style := styles.NoTTYStyleConfig
	if d.Color {
		style = styles.DarkStyleConfig
		// The terminal's own text color keeps body text readable on light backgrounds too.
		style.Document.Color = nil
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width), glamour.WithPreservedNewLines())
	if err != nil {
		return text
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	if !d.Links {
		rendered = glamourLinks.ReplaceAllString(rendered, "")
	}
	// glamour pads every line to the full width.
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}
