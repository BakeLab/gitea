// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Cell is one value in a Table. On color terminals Link, Color (six-digit RGB hex)
// and Bold style the text after alignment, so escape sequences never count toward
// the column width.
type Cell struct {
	Text, Link, Color string
	Bold              bool
}

// style applies the cell's link, color and emphasis to its already safe text.
func (c Cell) style(text string) string {
	return Hyperlink(c.Link, Emphasis(Colorize(c.Color, text, true), c.Bold), true)
}

// Row returns unstyled cells.
func Row(texts ...string) []Cell {
	cells := make([]Cell, len(texts))
	for i, text := range texts {
		cells[i].Text = text
	}
	return cells
}

// Table renders one line per row with columns aligned and separated by two spaces.
// Cell text passes through SafeLine, so server data can neither break the layout
// nor reach the terminal as escape sequences.
func Table(rows [][]Cell, color bool) string {
	texts := make([][]string, len(rows))
	var widths []int
	for r, row := range rows {
		texts[r] = make([]string, len(row))
		for i, cell := range row {
			texts[r][i] = SafeLine(cell.Text)
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], runewidth.StringWidth(texts[r][i]))
		}
	}
	var b strings.Builder
	for r, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			text := texts[r][i]
			if color {
				text = cell.style(text)
			}
			line.WriteString(text)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-runewidth.StringWidth(texts[r][i])+2))
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// Record is one item for human readers: a heading line of cells separated by spaces,
// followed by text lines such as a body and a URL.
type Record struct {
	Heading []Cell
	Lines   []string
}

// Render formats the record. Heading cells pass through SafeLine and are styled like
// Table cells; lines pass through SafeText, so they keep their own line breaks.
func (r Record) Render(color bool) string {
	var b strings.Builder
	for i, cell := range r.Heading {
		if i > 0 {
			b.WriteByte(' ')
		}
		text := SafeLine(cell.Text)
		if color {
			text = cell.style(text)
		}
		b.WriteString(text)
	}
	b.WriteByte('\n')
	for _, line := range r.Lines {
		b.WriteString(SafeText(line))
		b.WriteByte('\n')
	}
	return b.String()
}

// SafeLine reduces server text to one line: SafeText, with tabs and line breaks as spaces.
func SafeLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return ' '
		}
		return r
	}, SafeText(s))
}

// SafeText keeps tabs and line breaks in server text, normalizing CRLF to LF, and drops
// every other control or format character, such as escape sequences, lone carriage
// returns and bidirectional overrides, so the text cannot drive the terminal.
func SafeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\r\n", "\n"))
}
