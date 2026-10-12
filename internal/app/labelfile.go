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

// parseLabelFile reads "#color name ;description" rows, skipping blank and comment rows.
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
	return services.LabelOptions{Name: &name, Color: &color, Description: &description}, true, nil
}

// formatLabel renders a label as a label file row, rejecting labels the row cannot reproduce.
func formatLabel(label services.Label) (string, error) {
	color := strings.TrimPrefix(label.Color, "#")
	row := fmt.Sprintf("#%s %s ;%s", color, label.Name, label.Description)
	parsed, ok, err := parseLabelRow(row, 1)
	if err != nil || !ok || strings.ContainsAny(row, "\r\n") || *parsed.Name != label.Name || *parsed.Color != color || *parsed.Description != label.Description {
		return "", errors.New("label cannot be represented in the import format; use label export --json to preserve its text")
	}
	return row + "\n", nil
}
