// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// templateColors are the colors that the color functions know, as ANSI foreground codes.
var templateColors = map[string]int{"black": 30, "red": 31, "green": 32, "yellow": 33, "blue": 34, "magenta": 35, "cyan": 36, "white": 37, "default": 39}

// templateFuncs returns the functions that a --template can call besides those of text/template,
// the same names and meaning as the ones of gh.
func (o JSONOutput) templateFuncs(rows *[][]string) template.FuncMap {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	text := func(v any) string {
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	paint := func(style, input string) (string, error) {
		name, modifiers, _ := strings.Cut(style, "+")
		code, ok := templateColors[name]
		if !ok {
			return "", fmt.Errorf("color: unknown color %q; use one of black, red, green, yellow, blue, magenta, cyan, white or default, optionally followed by +b for bold, +d for dim, +u for underline or +h for bright", name)
		}
		var attributes []string
		for _, m := range modifiers {
			switch m {
			case 'b':
				attributes = append(attributes, "1")
			case 'd':
				attributes = append(attributes, "2")
			case 'u':
				attributes = append(attributes, "4")
			case 'h':
				if code < 90 && code != 39 {
					code += 60
				}
			default:
				return "", fmt.Errorf("color: unknown modifier %q in %q", m, style)
			}
		}
		attributes = append(attributes, strconv.Itoa(code))
		return "\x1b[" + strings.Join(attributes, ";") + "m" + input + "\x1b[0m", nil
	}
	parse := func(v any) (time.Time, error) {
		raw, ok := v.(string)
		if !ok {
			return time.Time{}, fmt.Errorf("a timestamp is a string, not %T", v)
		}
		t := ParseTime(raw)
		if t.IsZero() {
			return t, fmt.Errorf("%q is not an RFC 3339 timestamp", raw)
		}
		return t, nil
	}
	return template.FuncMap{
		"join": func(sep string, list any) (string, error) {
			items, ok := list.([]any)
			if !ok {
				return "", fmt.Errorf("join: want a list, got %T", list)
			}
			parts := make([]string, len(items))
			for i, item := range items {
				parts[i] = text(item)
			}
			return strings.Join(parts, sep), nil
		},
		"pluck": func(field string, list any) ([]any, error) {
			items, ok := list.([]any)
			if !ok {
				return nil, fmt.Errorf("pluck: want a list, got %T", list)
			}
			plucked := []any{}
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					if value, exists := object[field]; exists {
						plucked = append(plucked, value)
					}
				}
			}
			return plucked, nil
		},
		"tablerow": func(fields ...any) string {
			row := make([]string, len(fields))
			for i, field := range fields {
				row[i], _, _ = strings.Cut(text(field), "\n") // one line per row, like gh
				row[i] = strings.TrimSuffix(row[i], "\r")
			}
			*rows = append(*rows, row)
			return ""
		},
		"tablerender": func() string {
			table := renderRows(*rows)
			*rows = nil
			return table
		},
		"timeago": func(v any) (string, error) {
			t, err := parse(v)
			if err != nil {
				return "", fmt.Errorf("timeago: %w", err)
			}
			return RelativeTime(now(), t), nil
		},
		"timefmt": func(layout string, v any) (string, error) {
			t, err := parse(v)
			if err != nil {
				return "", fmt.Errorf("timefmt: %w", err)
			}
			return t.Format(layout), nil
		},
		"truncate": func(width int, v any) string {
			return ansi.Truncate(text(v), width, "…") // keeps the color of the color functions
		},
		"color": func(style string, v any) (string, error) { return paint(style, text(v)) },
		"autocolor": func(style string, v any) (string, error) {
			if !o.Color {
				return text(v), nil
			}
			return paint(style, text(v))
		},
		"hyperlink": func(url string, v any) string {
			return Hyperlink(url, text(v), o.Links && (strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://")))
		},
		"contains":  func(part, s string) bool { return strings.Contains(s, part) },
		"hasPrefix": func(prefix, s string) bool { return strings.HasPrefix(s, prefix) },
		"hasSuffix": func(suffix, s string) bool { return strings.HasSuffix(s, suffix) },
		"regexMatch": func(pattern, s string) (bool, error) {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return false, fmt.Errorf("regexMatch: %w", err)
			}
			return re.MatchString(s), nil
		},
	}
}

// renderRows aligns the cells of rows in columns two spaces apart, without trailing spaces.
func renderRows(rows [][]string) string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], runewidth.StringWidth(stripANSI(cell)))
		}
	}
	var b strings.Builder
	for _, row := range rows {
		line := make([]string, len(row))
		for i, cell := range row {
			line[i] = cell
			if i < len(row)-1 {
				line[i] += strings.Repeat(" ", widths[i]-runewidth.StringWidth(stripANSI(cell)))
			}
		}
		b.WriteString(strings.Join(line, "  ") + "\n")
	}
	return b.String()
}

var ansiEscape = regexp.MustCompile("\x1b(\\[[0-9;]*m|\\]8;;[^\x1b]*\x1b\\\\)")

// stripANSI removes the color and hyperlink sequences that the template functions add, which take
// no room on the screen.
func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// ValidateTemplate checks the syntax of a --template, so that a mistake fails before any request.
func ValidateTemplate(text string) error {
	var rows [][]string
	_, err := JSONOutput{Template: text}.parseTemplate(&rows)
	return err
}

func (o JSONOutput) parseTemplate(rows *[][]string) (*template.Template, error) {
	tmpl, err := template.New("").Funcs(o.templateFuncs(rows)).Parse(o.Template)
	if err != nil {
		return nil, fmt.Errorf("invalid --template: %w", err)
	}
	return tmpl, nil
}

// writeTemplate renders value, the selected JSON of a command, through the Go template of o.Template.
// A terminal gets no control characters from the server's text: they are removed from every string
// first, so only the template's own text and functions can drive the terminal.
func (o JSONOutput) writeTemplate(w io.Writer, value any) error {
	var rows [][]string
	tmpl, err := o.parseTemplate(&rows)
	if err != nil {
		return err
	}
	value = plainNumbers(value)
	if o.Pretty || o.Safe {
		value = safeValues(value)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, value); err != nil {
		return fmt.Errorf("evaluate --template: %w", err)
	}
	out.WriteString(renderRows(rows)) // what no tablerender showed, like gh
	_, err = w.Write(out.Bytes())
	return err
}

// safeValues returns value with SafeText applied to every string in it.
func safeValues(value any) any {
	switch v := value.(type) {
	case string:
		return SafeText(v)
	case []any:
		for i := range v {
			v[i] = safeValues(v[i])
		}
	case map[string]any:
		safe := make(map[string]any, len(v))
		for key, item := range v { // a key can be server text too, such as the units of a team
			safe[SafeText(key)] = safeValues(item)
		}
		return safe
	}
	return value
}

// plainNumbers returns value with each json.Number as an int64, or a float64 when it is not one, so
// that a template can test and compare numbers: json.Number is a string to text/template.
func plainNumbers(value any) any {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
		n, _ := v.Float64()
		return n
	case []any:
		for i := range v {
			v[i] = plainNumbers(v[i])
		}
	case map[string]any:
		for key, item := range v {
			v[key] = plainNumbers(item)
		}
	}
	return value
}
