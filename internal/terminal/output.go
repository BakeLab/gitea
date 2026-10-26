// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/itchyny/gojq"
)

// JSONOutput selects, filters, and formats command results as JSON.
type JSONOutput struct {
	// Fields holds the comma-separated fields to keep; "" and "*" keep every field.
	Fields string
	// Allowed lists the fields the command documents. A listed field that an item omits (an empty
	// omitempty value) is selected as null instead of being reported unknown; nil allows none.
	Allowed []string
	// Query is a jq expression applied after field selection.
	Query string
	// Pretty indents the JSON for a terminal and strips control characters from raw jq strings.
	Pretty bool
	// Safe strips control characters from raw jq strings and encoded text like Pretty, for terminal
	// output that must stay one line per value.
	Safe bool
	// Color highlights pretty JSON keys and values.
	Color bool
}

// Write projects explicit result fields of value, applies the jq filter, and writes the results.
func (o JSONOutput) Write(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	if names := fieldNames(o.Fields); names != nil {
		selectFields := func(item any) (any, error) {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("field selection requires resource objects")
			}
			selected := map[string]any{}
			for _, name := range names {
				v, exists := object[name]
				if !exists && slices.Contains(o.Allowed, name) {
					v, exists = nil, true
				}
				if !exists {
					return nil, fmt.Errorf("unknown JSON field %q", name)
				}
				selected[name] = v
			}
			return selected, nil
		}
		switch v := normalized.(type) {
		case []any:
			for i := range v {
				v[i], err = selectFields(v[i])
				if err != nil {
					return err
				}
			}
		default:
			normalized, err = selectFields(v)
			if err != nil {
				return err
			}
		}
	}
	if o.Query == "" {
		return o.encode(w, normalized)
	}
	parsed, err := parseQuery(o.Query)
	if err != nil {
		return err
	}
	iter := parsed.Run(normalized)
	for {
		value, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, ok := value.(error); ok {
			if halt, ok := errors.AsType[*gojq.HaltError](err); ok && halt.Value() == nil {
				return nil
			}
			return fmt.Errorf("evaluate --jq: %w", err)
		}
		if str, ok := value.(string); ok {
			if o.Pretty || o.Safe {
				str = SafeText(str)
			}
			if _, err := fmt.Fprintln(w, str); err != nil {
				return err
			}
		} else if err := o.encode(w, value); err != nil {
			return err
		}
	}
}

// encode writes one JSON value followed by a newline.
func (o JSONOutput) encode(w io.Writer, value any) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if o.Pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return err
	}
	text := buf.String()
	if o.Pretty || o.Safe {
		text = escapeJSONText(text)
	}
	if o.Color {
		text = colorJSON(text)
	}
	_, err := io.WriteString(w, text)
	return err
}

// IndentJSON formats JSON text for a terminal like Write with Pretty, but keeps the key order of the
// text: indented, sanitized, ending with a newline and, with color, highlighted.
func IndentJSON(data []byte, color bool) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(data), "", "  "); err != nil {
		return "", err
	}
	text := escapeJSONText(buf.String()) + "\n"
	if color {
		text = colorJSON(text)
	}
	return text, nil
}

// escapeJSONText escapes the control and format characters left in encoded JSON text, such as DEL,
// C1 controls and bidirectional overrides, which the encoder writes as they are. JSON text holds them
// only inside strings, where \uXXXX stands for the same character, so the text cannot drive the
// terminal and still decodes to the same data. The line breaks and tabs of indentation stay.
func escapeJSONText(text string) string {
	if !strings.ContainsFunc(text, needsJSONEscape) {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if !needsJSONEscape(r) {
			b.WriteRune(r)
			continue
		}
		if r1, r2 := utf16.EncodeRune(r); r1 != unicode.ReplacementChar {
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		} else {
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// needsJSONEscape reports whether r is a control or format character other than a line break or tab.
func needsJSONEscape(r rune) bool {
	return r != '\t' && r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}

// JSON colors, following gh: bold blue keys, green strings, yellow booleans, cyan null.
const (
	jsonKeyColor    = "\x1b[1;34m"
	jsonStringColor = "\x1b[32m"
	jsonBoolColor   = "\x1b[33m"
	jsonNullColor   = "\x1b[36m"
	jsonResetColor  = "\x1b[0m"
)

// colorJSON highlights the keys and literal values of encoded JSON text.
func colorJSON(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		color, end := "", i+1
		switch c := text[i]; {
		case c == '"':
			for end < len(text) && text[end] != '"' {
				if text[end] == '\\' {
					end++
				}
				end++
			}
			end++
			color = jsonStringColor
			if strings.HasPrefix(strings.TrimLeft(text[end:], " "), ":") {
				color = jsonKeyColor
			}
		case strings.HasPrefix(text[i:], "true"):
			color, end = jsonBoolColor, i+len("true")
		case strings.HasPrefix(text[i:], "false"):
			color, end = jsonBoolColor, i+len("false")
		case strings.HasPrefix(text[i:], "null"):
			color, end = jsonNullColor, i+len("null")
		}
		if color == "" {
			b.WriteString(text[i:end])
		} else {
			b.WriteString(color + text[i:end] + jsonResetColor)
		}
		i = end
	}
	return b.String()
}

// ValidateOutput catches invalid projection/filter syntax before a service mutates data.
// An unknown field error lists the allowed fields.
func ValidateOutput(fields, query string, allowed []string) error {
	if names := fieldNames(fields); names != nil && allowed != nil {
		for _, name := range names {
			if name == "" {
				return errors.New("--json field names cannot be empty; use --json alone for every field")
			}
			if !slices.Contains(allowed, name) {
				return fmt.Errorf("unknown JSON field %q\navailable fields: %s", name, strings.Join(allowed, ", "))
			}
		}
	}
	if query != "" {
		if _, err := parseQuery(query); err != nil {
			return err
		}
	}
	return nil
}

// fieldNames returns the selected JSON fields, or nil when every field is selected.
func fieldNames(fields string) []string {
	if fields == "" || fields == "*" {
		return nil
	}
	names := strings.Split(fields, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	return names
}

func parseQuery(query string) (*gojq.Query, error) {
	parsed, err := gojq.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("invalid --jq expression: %w", err)
	}
	return parsed, nil
}
