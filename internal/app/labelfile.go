// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
)

var labelColor = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// parseLabelFile reads "#color name ;description ;flags" rows, skipping blank and comment rows. The
// description and the flags, a comma-separated list of exclusive and archived, are optional.
func parseLabelFile(r io.Reader) ([]services.LabelOptions, error) {
	var labels []services.LabelOptions
	scanner := bufio.NewScanner(r)
	for line := 1; scanner.Scan(); line++ {
		label, ok, err := parseLabelRow(scanner.Text(), line)
		if err != nil {
			return nil, err
		}
		if ok {
			labels = append(labels, label)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(labels) == 0 {
		return nil, clierr.Wrap(clierr.Usage, errors.New("label file contains no labels"))
	}
	return labels, nil
}

func parseLabelRow(text string, line int) (services.LabelOptions, bool, error) {
	raw := strings.TrimSpace(text)
	parts := strings.SplitN(raw, ";", 2)
	fields := strings.Fields(parts[0])
	var exclusive, archived bool
	if len(parts) == 2 {
		if at := strings.LastIndex(parts[1], ";"); at >= 0 {
			if flags, ok := labelFlags(parts[1][at+1:]); ok {
				exclusive, archived, parts[1] = flags["exclusive"], flags["archived"], parts[1][:at]
			}
		}
	}
	// Rows start with an optional "#" before the color, as Gitea's label templates do.
	if raw == "" || strings.HasPrefix(raw, "#") && !labelColor.MatchString(strings.TrimPrefix(fields[0], "#")) {
		return services.LabelOptions{}, false, nil
	}
	if len(fields) < 2 {
		return services.LabelOptions{}, false, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid label row %d", line))
	}
	color, name, description := strings.TrimPrefix(fields[0], "#"), strings.Join(fields[1:], " "), ""
	if len(parts) == 2 {
		description = strings.TrimSpace(parts[1])
	}
	if !labelColor.MatchString(color) {
		return services.LabelOptions{}, false, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid color on row %d", line))
	}
	// Both flags are always sent, so that importing a row without them clears them with --force.
	return services.LabelOptions{Name: &name, Color: &color, Description: &description, Exclusive: &exclusive, Archived: &archived}, true, nil
}

// labelFlags parses the flags field of a label row, such as "exclusive,archived".
func labelFlags(field string) (map[string]bool, bool) {
	flags := map[string]bool{}
	for flag := range strings.SplitSeq(field, ",") {
		flag = strings.TrimSpace(flag)
		if flag != "exclusive" && flag != "archived" {
			return nil, false
		}
		flags[flag] = true
	}
	return flags, true
}

// formatLabel renders a label as a label file row, rejecting labels the row cannot reproduce.
func formatLabel(label services.Label) (string, error) {
	color := strings.TrimPrefix(label.Color, "#")
	row := fmt.Sprintf("#%s %s", color, label.Name)
	var flags []string
	if label.Exclusive {
		flags = append(flags, "exclusive")
	}
	if label.Archived {
		flags = append(flags, "archived")
	}
	if label.Description != "" || len(flags) > 0 {
		row += " ;" + label.Description
	}
	if len(flags) > 0 {
		row += " ;" + strings.Join(flags, ",")
	}
	parsed, ok, err := parseLabelRow(row, 1)
	if err != nil || !ok || strings.ContainsAny(row, "\r\n") || *parsed.Name != label.Name || *parsed.Color != color ||
		*parsed.Description != label.Description || *parsed.Exclusive != label.Exclusive || *parsed.Archived != label.Archived {
		return "", errors.New("label cannot be represented in the import format; use label export --json to preserve its text")
	}
	return row + "\n", nil
}
