// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"
	"unicode"
)

// ColorEnabled resolves the color preference against terminal and environment policy.
// auto colors TTY stdout unless NO_COLOR is set or TERM is dumb; always forces color; never disables it.
func ColorEnabled(preference string, outTTY bool, getenv func(string) string) bool {
	switch preference {
	case "always":
		return true
	case "never":
		return false
	default: // auto and unknown fall back to terminal detection
		if getenv != nil && (getenv("NO_COLOR") != "" || getenv("TERM") == "dumb") {
			return false
		}
		return outTTY
	}
}

// HyperlinksEnabled resolves the hyperlinks preference like ColorEnabled, but independently of color:
// NO_COLOR keeps links, and auto links only on TTY stdout whose TERM is not dumb.
func HyperlinksEnabled(preference string, outTTY bool, getenv func(string) string) bool {
	switch preference {
	case "always":
		return true
	case "never":
		return false
	default:
		return outTTY && (getenv == nil || getenv("TERM") != "dumb")
	}
}

// namedColors are the ANSI 16 colors for states and status, so they follow the terminal's own theme.
var namedColors = map[string]string{"red": "31", "green": "32", "yellow": "33", "blue": "34", "magenta": "35", "cyan": "36", "muted": "90"}

// Colorize colors text by name (red, green, yellow, blue, magenta, cyan or muted) from the terminal's
// ANSI 16 palette. Other colors, such as label hex colors, leave the text plain.
func Colorize(color, text string, enabled bool) string {
	code, ok := namedColors[color]
	if !enabled || !ok || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Emphasis applies simple bold styling for TTY human tables.
func Emphasis(text string, enabled bool) string {
	if !enabled || text == "" {
		return text
	}
	return "\x1b[1m" + text + "\x1b[0m"
}

// Hyperlink wraps text in an OSC 8 terminal hyperlink to url. URLs with control
// characters are left unlinked so server data cannot inject escape sequences.
func Hyperlink(url, text string, enabled bool) string {
	if !enabled || url == "" || text == "" || strings.ContainsFunc(url, unicode.IsControl) {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
