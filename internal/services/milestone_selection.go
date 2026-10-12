// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"
)

// All returns the open and closed milestones of the repository.
func (s Milestones) All(ctx context.Context) ([]Milestone, error) {
	return listAll(func(page int) ([]Milestone, error) { return s.List(ctx, "all", 100, page) })
}

// Find returns the milestones that raws address among the open and closed milestones, each by ID
// or title (D10); byName matches numeric values as titles only.
func (s Milestones) Find(ctx context.Context, raws []string, byName bool) ([]Milestone, error) {
	items, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	finder := NameOrID[Milestone]{Kind: "milestone", Scope: s.Repository, ID: func(m Milestone) int64 { return m.ID }, Name: func(m Milestone) string { return m.Title }, ByName: byName}
	result := make([]Milestone, len(raws))
	for i, raw := range raws {
		if result[i], err = finder.Find(items, raw); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// FindOne returns the milestone that raw addresses like Find.
func (s Milestones) FindOne(ctx context.Context, raw string, byName bool) (Milestone, error) {
	items, err := s.Find(ctx, []string{raw}, byName)
	if err != nil {
		return Milestone{}, err
	}
	return items[0], nil
}

// FilterIDs resolves the milestones that filter an issue listing, each an ID or a title, to their
// IDs. Gitea drops filter values that name no milestone and lists every issue when none is left,
// so an unknown milestone is an error here.
func (s Milestones) FilterIDs(ctx context.Context, values []string) ([]string, error) {
	items, err := s.Find(ctx, values, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = strconv.FormatInt(item.ID, 10)
	}
	return ids, nil
}
