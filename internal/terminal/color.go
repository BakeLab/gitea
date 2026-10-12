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

// TrueColor reports whether the terminal advertises 24-bit color through COLORTERM.
func TrueColor(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	value := getenv("COLORTERM")
	return value == "truecolor" || value == "24bit"
}

// namedColors are the ANSI 16 colors for states and status, so they follow the terminal's own theme.
var namedColors = map[string]string{"red": "31", "green": "32", "yellow": "33", "blue": "34", "magenta": "35", "cyan": "36", "muted": "90"}

// Colorize colors text by name (red, green, yellow, blue, magenta, cyan or muted) from the terminal's palette,
// or by six-digit RGB hex with 24-bit color. Unknown colors leave the text plain.
func Colorize(color, text string, enabled bool) string {
	if !enabled {
		return text
	}
	return paint(color, text, true)
}

// paint is Colorize for enabled color; without trueColor, hex colors use the nearest 256-color index.
func paint(color, text string, trueColor bool) string {
	if text == "" {
		return text
	}
	if code, ok := namedColors[color]; ok {
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
	r, g, b, ok := parseHex(color)
	switch {
	case !ok:
		return text
	case trueColor:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, text)
	default:
		return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[0m", ansi256(r, g, b), text)
	}
}

func parseHex(hex string) (r, g, b int, ok bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	value, err := strconv.ParseUint(hex, 16, 24)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(value >> 16), int(value >> 8 & 0xff), int(value & 0xff), true
}

// ansi256 returns the 256-color palette index nearest to an RGB color: one of the 6×6×6 cube colors
// (16–231) or one of the 24 grays (232–255).
func ansi256(r, g, b int) int {
	levels := [6]int{0, 95, 135, 175, 215, 255}
	level := func(v int) int {
		switch {
		case v < 48:
			return 0
		case v < 115:
			return 1
		default:
			return (v - 35) / 40
		}
	}
	ri, gi, bi := level(r), level(g), level(b)
	gray := min(max((r+g+b)/3-3, 0)/10, 23)
	grayValue := 8 + 10*gray
	distance := func(cr, cg, cb int) int { return (r-cr)*(r-cr) + (g-cg)*(g-cg) + (b-cb)*(b-cb) }
	if distance(levels[ri], levels[gi], levels[bi]) <= distance(grayValue, grayValue, grayValue) {
		return 16 + 36*ri + 6*gi + bi
	}
	return 232 + gray
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
