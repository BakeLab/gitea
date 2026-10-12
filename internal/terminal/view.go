// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Field is one labeled fact in a View.
type Field struct {
	Label string
	// Value is plain text. Cells, when set, replace it with styled values joined by commas, such
	// as colored labels.
	Value string
	Cells []Cell
	// TTYOnly and PipedOnly restrict the field to one kind of output, like table columns.
	TTYOnly, PipedOnly bool
}

// text returns the field's safe value, styled for d when it is made of cells.
func (f Field) text(d Display) string {
	if len(f.Cells) == 0 {
		return SafeLine(f.Value)
	}
	values := make([]string, len(f.Cells))
	for i, cell := range f.Cells {
		values[i] = SafeLine(cell.Text)
		if d.TTY {
			values[i] = cell.style(values[i], d, false)
		}
	}
	return strings.Join(values, ", ")
}

// View is one resource for human readers, such as an issue, a release or a repository.
//
// On a TTY it shows the bold title with its muted ID, a line of badges and muted metadata, the
// aligned non-empty fields, the body (markdown rendered when the display allows), and the footer
// "View this <kind> on Gitea: <url>". Otherwise it writes "key:\tvalue" lines for the title, the
// fields and the URL, then "--" and the raw body, for scripts.
type View struct {
	Title, ID string
	// Badges, such as the state, start the line under the title; Meta completes it, for example
	// "alice opened about 2 hours ago • 2 comments".
	Badges []Cell
	Meta   string
	Fields []Field
	Body   string
	// Bodyless resources, such as webhooks, have no body: the view ends with the fields and URL.
	Bodyless bool
	// Kind names the resource in the footer, such as "issue".
	Kind, URL string
}

// Render formats the view for d. All server text is sanitized.
func (v View) Render(d Display) string {
	var b strings.Builder
	if !d.TTY {
		if v.Title != "" {
			b.WriteString("title:\t" + SafeLine(v.Title) + "\n")
		}
		for _, field := range v.Fields {
			if !field.TTYOnly {
				b.WriteString(strings.ToLower(SafeLine(field.Label)) + ":\t" + field.text(d) + "\n")
			}
		}
		if v.URL != "" {
			b.WriteString("url:\t" + SafeLine(v.URL) + "\n")
		}
		if v.Bodyless {
			return b.String()
		}
		body := SafeText(v.Body)
		b.WriteString("--\n" + body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteByte('\n')
		}
		return b.String()
	}

	muted := func(text string) string { return Cell{Muted: true}.style(text, d, false) }
	b.WriteString(Cell{Bold: true}.style(SafeLine(v.Title), d, false))
	if v.ID != "" {
		b.WriteString(" " + muted(SafeLine(v.ID)))
	}
	b.WriteByte('\n')
	var summary []string
	for _, badge := range v.Badges {
		summary = append(summary, badge.style(SafeLine(badge.Text), d, false))
	}
	if meta := SafeLine(v.Meta); meta != "" {
		summary = append(summary, muted(meta))
	}
	if len(summary) > 0 {
		b.WriteString(strings.Join(summary, " • ") + "\n")
	}

	var fields []Field
	labelWidth := 0
	for _, field := range v.Fields {
		if !field.PipedOnly && (field.Value != "" || len(field.Cells) > 0) {
			fields = append(fields, field)
			labelWidth = max(labelWidth, runewidth.StringWidth(SafeLine(field.Label)))
		}
	}
	for _, field := range fields {
		label := SafeLine(field.Label) + ":"
		b.WriteString(Cell{Bold: true}.style(label, d, false) + strings.Repeat(" ", labelWidth+2-runewidth.StringWidth(label)) + field.text(d) + "\n")
	}

	if !v.Bodyless {
		body := strings.TrimSpace(v.Body)
		switch {
		case body == "":
			body = muted("No description provided")
		case d.Markdown:
			body = Markdown(body, d)
		default:
			body = SafeText(body)
		}
		b.WriteString("\n" + body + "\n")
	}
	if v.URL != "" {
		kind := "View on Gitea: "
		if v.Kind != "" {
			kind = "View this " + v.Kind + " on Gitea: "
		}
		b.WriteString("\n" + muted(kind+SafeLine(v.URL)) + "\n")
	}
	return b.String()
}
