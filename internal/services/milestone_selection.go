// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// All returns the open and closed milestones of the repository.
func (s Milestones) All(ctx context.Context) ([]Milestone, error) {
	return listAll(func(page int) ([]Milestone, error) { return s.List(ctx, "all", 100, page) })
}

// Find returns the milestones that raws address among the open and closed milestones, each by ID
// or title; byName matches numeric values as titles only.
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

// FilterIDs resolves the milestones that filter an issue listing, each an ID or a title, to the
// values the issues API reads back as the same milestones: Gitea matches a filter value by title
// first and by ID only then, so a unique title without a comma is sent as is, and an ID only when
// no title reads like it. Gitea drops filter values that name no milestone and lists every issue
// when none is left, so an unknown milestone is an error here.
func (s Milestones) FilterIDs(ctx context.Context, values []string) ([]string, error) {
	items, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	titles := make(map[string]int, len(items))
	for _, item := range items {
		titles[item.Title]++
	}
	finder := NameOrID[Milestone]{Kind: "milestone", Scope: s.Repository, ID: func(m Milestone) int64 { return m.ID }, Name: func(m Milestone) string { return m.Title }}
	result := make([]string, len(values))
	for i, raw := range values {
		item, err := finder.Find(items, raw)
		if err != nil {
			return nil, err
		}
		id := strconv.FormatInt(item.ID, 10)
		switch {
		case titles[item.Title] == 1 && !strings.Contains(item.Title, ","):
			result[i] = item.Title
		case titles[id] == 0:
			result[i] = id
		default:
			return nil, fmt.Errorf("milestone %q (ID %s) cannot filter issues: another milestone of %s is titled %q", item.Title, id, s.Repository, id)
		}
	}
	return result, nil
}
