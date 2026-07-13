// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package theme

import (
	"os"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// defaultDarkBackground is the background to assume when we cannot detect one. It
// matches the default lipgloss falls back to.
const defaultDarkBackground = true

// HasDarkBackground reports whether the terminal has a dark background.
//
// It only asks the terminal when stdin and stdout are both terminals. Detection
// works by writing an escape sequence to the output and waiting for the terminal
// to answer on the input, and nothing answers when stdio is redirected, so asking
// means waiting on a reply that never comes. On Windows that wait is unbounded:
// lipgloss opens the console directly rather than giving up, which is why tea used
// to hang at start-up under a service or a CI runner.
func HasDarkBackground() bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return defaultDarkBackground
	}

	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}
