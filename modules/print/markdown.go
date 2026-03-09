// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"fmt"
	"os"

	"charm.land/glamour/v2"
	"golang.org/x/term"
)

// outputMarkdown prints markdown to stdout, formatted for terminals.
// If the input could not be parsed, it is printed unformatted, the error
// is returned anyway.
func outputMarkdown(markdown string, baseURL string) error {
	var renderer *glamour.TermRenderer
	var err error
	if IsInteractive() {
		renderer, err = glamour.NewTermRenderer(
			glamour.WithBaseURL(baseURL),
			glamour.WithPreservedNewLines(),
			glamour.WithWordWrap(getWordWrap()),
		)
	} else {
		renderer, err = glamour.NewTermRenderer(
			glamour.WithStandardStyle("notty"),
			glamour.WithBaseURL(baseURL),
			glamour.WithPreservedNewLines(),
			glamour.WithWordWrap(getWordWrap()),
		)
	}

	if err != nil {
		fmt.Print(markdown)
		return err
	}

	out, err := renderer.Render(markdown)
	if err != nil {
		fmt.Print(markdown)
		return err
	}
	fmt.Print(out)
	return nil
}

// stolen from https://github.com/charmbracelet/glow/blob/e9d728c/main.go#L152-L165
func getWordWrap() int {
	fd := int(os.Stdout.Fd())
	width := 80
	if term.IsTerminal(fd) {
		if w, _, err := term.GetSize(fd); err == nil {
			width = w
		}
	}
	if width > 120 {
		width = 120
	}
	return width
}
