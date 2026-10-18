// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
)

// MarkdownEnabled resolves the markdown preference: raw never renders, and auto renders on TTY stdout.
func MarkdownEnabled(preference string, outTTY bool) bool {
	return outTTY && preference != "raw"
}

// glamourLinks matches the OSC 8 hyperlinks that glamour writes for markdown links.
var glamourLinks = regexp.MustCompile("\x1b\\]8;[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// numericReference matches a decimal or hexadecimal character reference, with or without the semicolon.
var numericReference = regexp.MustCompile(`&#([0-9]{1,8}|[xX][0-9a-fA-F]{1,8});?`)

// dropControlReference returns "" for a character reference that names a control or format character
// other than a tab or line break, and the reference itself otherwise.
func dropControlReference(reference string) string {
	digits := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(reference, "&#"), "x"), ";")
	digits = strings.TrimPrefix(digits, "X")
	base := 10
	if strings.HasPrefix(reference, "&#x") || strings.HasPrefix(reference, "&#X") {
		base = 16
	}
	n, err := strconv.ParseInt(digits, base, 32)
	if err != nil {
		return reference
	}
	r := rune(n)
	if r != '\t' && r != '\n' && (r == 0 || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
		return ""
	}
	return reference
}

// glamourSequences matches the escape sequences glamour writes itself: colors and OSC 8 hyperlinks.
var glamourSequences = regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;[^\x07\x1b]*(?:\x07|\x1b\\\\)")

// stripControls removes every control character from rendered output except newlines and the
// sequences glamour writes, which markdown cannot smuggle in after the input was sanitized.
func stripControls(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if loc := glamourSequences.FindStringIndex(text[i:]); loc != nil && loc[0] == 0 {
			b.WriteString(text[i : i+loc[1]])
			i += loc[1]
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\n' || r == '\t' || !(r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
			b.WriteString(text[i : i+size])
		}
		i += size
	}
	return b.String()
}

// renderers caches a glamour renderer per width and color, as building one parses its style each
// time; the lock also serializes rendering.
var renderers = struct {
	sync.Mutex
	byKey map[[2]int]*glamour.TermRenderer
}{byKey: map[[2]int]*glamour.TermRenderer{}}

func markdownRenderer(width int, color bool) (*glamour.TermRenderer, error) {
	key := [2]int{width, 0}
	if color {
		key[1] = 1
	}
	if renderer := renderers.byKey[key]; renderer != nil {
		return renderer, nil
	}
	style := styles.NoTTYStyleConfig
	if color {
		style = styles.DarkStyleConfig
		// The terminal's own text color keeps body text readable on light backgrounds too.
		style.Document.Color = nil
	}
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width), glamour.WithPreservedNewLines())
	if err == nil {
		renderers.byKey[key] = renderer
	}
	return renderer, err
}

// Markdown renders sanitized markdown for d: wrapped to its width (80 when unknown), colored or plain,
// with links only when d allows them. It keeps line breaks like Gitea does, and returns the sanitized
// text unrendered if rendering fails.
func Markdown(text string, d Display) string {
	// Markdown decodes numeric character references such as &#27; into the characters they name,
	// which SafeText cannot see, so drop the ones that name a control character before rendering.
	// Other entities (&amp;, &section=...) are left to the renderer so ordinary text is unchanged.
	text = numericReference.ReplaceAllStringFunc(text, dropControlReference)
	text = SafeText(text)
	width := d.Width
	if width <= 0 {
		width = 80
	}
	renderers.Lock()
	renderer, err := markdownRenderer(width, d.Color)
	if err != nil {
		renderers.Unlock()
		return text
	}
	rendered, err := renderer.Render(text)
	renderers.Unlock()
	if err != nil {
		return text
	}
	rendered = stripControls(rendered)
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
