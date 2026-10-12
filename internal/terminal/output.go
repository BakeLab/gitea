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

	"github.com/itchyny/gojq"
)

// JSONOutput selects, filters, and formats command results as JSON.
type JSONOutput struct {
	// Fields holds the comma-separated fields to keep; "" and "*" keep every field.
	Fields string
	// Query is a jq expression applied after field selection.
	Query string
	// Pretty indents the JSON for a terminal and strips control characters from raw jq strings.
	Pretty bool
	// Color highlights pretty JSON keys and values.
	Color bool
}

// WriteJSON projects explicit result fields and applies an in-process jq filter.
func WriteJSON(w io.Writer, value any, fields, query string) error {
	return JSONOutput{Fields: fields, Query: query}.Write(w, value)
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
			return fmt.Errorf("evaluate --jq: %w", err)
		}
		if str, ok := value.(string); ok {
			if o.Pretty {
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
	if o.Pretty {
		// The encoder escapes control characters but not format characters such as bidirectional overrides.
		text = SafeText(text)
	}
	if o.Color {
		text = colorJSON(text)
	}
	_, err := io.WriteString(w, text)
	return err
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
