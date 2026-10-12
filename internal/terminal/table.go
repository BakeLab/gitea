// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
)

// Display describes the output that tables and views render for. On a terminal (TTY) they align,
// truncate and style text for people; otherwise they write plain tab-separated or key/value text
// for scripts, without header, color or links.
type Display struct {
	TTY bool
	// Color enables SGR colors and emphasis on a TTY; TrueColor writes hex colors in 24-bit instead
	// of the nearest of 256.
	Color, TrueColor bool
	// Links enables OSC 8 links on the first table column on a TTY.
	Links bool
	// Markdown renders view bodies on a TTY.
	Markdown bool
	// Width is the terminal width that table rows fit in; 0 disables truncation.
	Width int
	// Now is the reference for relative times; zero means the current time.
	Now time.Time
}

// Column describes one table column.
type Column struct {
	// Header names the column; a TTY shows it uppercased above the rows.
	Header string
	// Flex columns shrink to fit the terminal width, widest first, cutting text with "…", down to
	// Min, the header width or eight cells, whichever is wider. Other columns are never cut.
	Flex bool
	Min  int
	// TTYOnly and PipedOnly restrict the column to one kind of output.
	TTYOnly, PipedOnly bool
	// Prefix precedes non-empty cells on a TTY, such as "#" before issue numbers.
	Prefix string
}

// Cell is one value in a Table. On color terminals Color (a name such as green, or six-digit
// RGB hex), Bold and Muted style the text after alignment, so escape sequences never count
// toward the column width. Link applies to the first column only.
type Cell struct {
	Text, Link, Color string
	Bold, Muted       bool
	// Time, when set, replaces Text: relative on a TTY, RFC3339 UTC otherwise.
	Time time.Time
}

// text returns the cell's safe, unstyled text for the display.
func (c Cell) text(column Column, d Display) string {
	if !c.Time.IsZero() {
		if !d.TTY {
			return Timestamp(c.Time)
		}
		now := d.Now
		if now.IsZero() {
			now = time.Now()
		}
		return RelativeTime(now, c.Time)
	}
	text := SafeLine(c.Text)
	if d.TTY && text != "" {
		text = column.Prefix + text
	}
	return text
}

// style applies the cell's color, emphasis and, when link is set, its hyperlink to already safe text.
func (c Cell) style(text string, d Display, link bool) string {
	if d.Color {
		color := c.Color
		if c.Muted {
			color = "muted"
		}
		text = Emphasis(paint(color, text, d.TrueColor), c.Bold)
	}
	return Hyperlink(c.Link, text, link && d.Links)
}

// Row returns unstyled cells.
func Row(texts ...string) []Cell {
	cells := make([]Cell, len(texts))
	for i, text := range texts {
		cells[i].Text = text
	}
	return cells
}

// Table renders rows under columns; rows may have more cells than there are columns. On a TTY it
// prints an uppercase header row when any column has a header, aligns columns two spaces apart,
// and shrinks Flex columns until rows fit the display width. Otherwise it writes one line of
// tab-separated cells per row. Cell text passes through SafeLine either way, so server data can
// neither break the layout nor reach the terminal as escape sequences. No rows render as nothing.
func Table(columns []Column, rows [][]Cell, d Display) string {
	if len(rows) == 0 {
		return ""
	}
	column := func(i int) Column {
		if i < len(columns) {
			return columns[i]
		}
		return Column{}
	}
	count := len(columns)
	for _, row := range rows {
		count = max(count, len(row))
	}
	var shown []int
	for i := range count {
		if c := column(i); !(c.TTYOnly && !d.TTY) && !(c.PipedOnly && d.TTY) {
			shown = append(shown, i)
		}
	}
	texts := make([][]string, len(rows))
	for r, row := range rows {
		texts[r] = make([]string, len(shown))
		for j, i := range shown {
			if i < len(row) {
				texts[r][j] = row[i].text(column(i), d)
			}
		}
	}
	var b strings.Builder
	if !d.TTY {
		for _, line := range texts {
			b.WriteString(strings.Join(line, "\t"))
			b.WriteByte('\n')
		}
		return b.String()
	}

	headers := make([]string, len(shown))
	hasHeader := false
	for j, i := range shown {
		headers[j] = strings.ToUpper(SafeLine(column(i).Header))
		hasHeader = hasHeader || headers[j] != ""
	}
	widths := make([]int, len(shown))
	for j := range shown {
		if hasHeader {
			widths[j] = runewidth.StringWidth(headers[j])
		}
		for _, line := range texts {
			widths[j] = max(widths[j], runewidth.StringWidth(line[j]))
		}
	}
	fitWidth(widths, d.Width, func(j int) (bool, int) {
		c := column(shown[j])
		return c.Flex, max(c.Min, runewidth.StringWidth(headers[j]), 8)
	})

	writeLine := func(cells []string, style func(j int, text string) string) {
		var line strings.Builder
		for j, text := range cells {
			if column(shown[j]).Flex {
				text = runewidth.Truncate(text, widths[j], "…")
			}
			line.WriteString(style(j, text))
			if j < len(cells)-1 {
				line.WriteString(strings.Repeat(" ", widths[j]-runewidth.StringWidth(text)+2))
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	if hasHeader {
		writeLine(headers, func(_ int, text string) string { return Cell{Muted: true}.style(text, d, false) })
	}
	for r, row := range rows {
		writeLine(texts[r], func(j int, text string) string {
			if i := shown[j]; i < len(row) {
				return row[i].style(text, d, j == 0)
			}
			return text
		})
	}
	return b.String()
}

// fitWidth narrows flexible columns, always the widest one first, until the row and its two-space
// gaps fit limit or every flexible column reached its floor. A limit of 0 disables fitting.
func fitWidth(widths []int, limit int, flex func(j int) (bool, int)) {
	if limit <= 0 {
		return
	}
	excess := 2 * (len(widths) - 1)
	for _, w := range widths {
		excess += w
	}
	excess -= limit
	for ; excess > 0; excess-- {
		widest := -1
		for j, w := range widths {
			if ok, floor := flex(j); ok && w > floor && (widest < 0 || w > widths[widest]) {
				widest = j
			}
		}
		if widest < 0 {
			return
		}
		widths[widest]--
	}
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
	d := Display{TTY: true, Color: color, TrueColor: true, Links: color}
	var b strings.Builder
	for i, cell := range r.Heading {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(cell.style(SafeLine(cell.Text), d, true))
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
