// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package terminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/itchyny/gojq"
)

// WriteJSON projects explicit result fields and applies an in-process jq filter.
func WriteJSON(w io.Writer, value any, fields, query string) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	if names := fieldNames(fields); names != nil {
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
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if query == "" {
		return encoder.Encode(normalized)
	}
	parsed, err := parseQuery(query)
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
			if _, err := fmt.Fprintln(w, str); err != nil {
				return err
			}
		} else if err := encoder.Encode(value); err != nil {
			return err
		}
	}
}

// ValidateOutput catches invalid projection/filter syntax before a service mutates data.
func ValidateOutput(fields, query string, allowed []string) error {
	if names := fieldNames(fields); names != nil && allowed != nil {
		known := map[string]bool{}
		for _, name := range allowed {
			known[name] = true
		}
		for _, name := range names {
			if !known[name] {
				return fmt.Errorf("unknown JSON field %q", name)
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
