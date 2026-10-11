// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"strings"
)

// ResolveName selects an exact title across open and closed milestones.
func (s Milestones) ResolveName(ctx context.Context, name string) (int64, error) {
	if strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("milestone name cannot be empty")
	}
	items, err := listAll(func(page int) ([]Milestone, error) { return s.List(ctx, "all", 100, page) })
	if err != nil {
		return 0, err
	}
	var id int64
	for _, item := range items {
		if item.Title != name {
			continue
		}
		if id != 0 {
			return 0, fmt.Errorf("milestone name %q is ambiguous; select its numeric ID", name)
		}
		id = item.ID
	}
	if id == 0 {
		return 0, fmt.Errorf("milestone %q does not exist; use tea milestone list --state all", name)
	}
	return id, nil
}
