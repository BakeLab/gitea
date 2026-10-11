// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"fmt"
	"strconv"
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

// Colorize applies a 24-bit foreground color from a six-digit RGB hex value.
func Colorize(hex, text string, enabled bool) string {
	if !enabled || text == "" {
		return text
	}
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return text
	}
	r, errR := strconv.ParseUint(hex[0:2], 16, 8)
	g, errG := strconv.ParseUint(hex[2:4], 16, 8)
	b, errB := strconv.ParseUint(hex[4:6], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return text
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, text)
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
