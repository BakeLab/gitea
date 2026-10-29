// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gitea "gitea.dev/sdk"
)

// All returns the open and closed milestones of the repository.
func (s Milestones) All(ctx context.Context) ([]Milestone, error) { return s.AllIn(ctx, "all") }

// AllIn returns every milestone in state: open, closed or all.
func (s Milestones) AllIn(ctx context.Context, state string) ([]Milestone, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	milestones, err := callAll(ctx, s.Client, 100, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Milestone, *gitea.Response, error) {
		return c.Repositories.ListMilestones(ctx, owner, repo, gitea.ListMilestoneOption{ListOptions: list, State: gitea.StateType(state)})
	})
	if err != nil {
		// A 404 here can only mean the repository is missing or invisible.
		return nil, Classify(err, "repository", s.Repository, "")
	}
	return convertAll(milestones, s.from), nil
}

// Find returns the milestones that raws address among the open and closed milestones, each by ID
// or title; byName matches numeric values as titles only.
func (s Milestones) Find(ctx context.Context, raws []string, byName bool) ([]Milestone, error) {
	items, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Milestone, len(raws))
	for i, raw := range raws {
		if result[i], err = s.FindIn(items, raw, byName); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// FindIn returns the milestone among items, the milestones of the repository, that raw addresses
// like Find.
func (s Milestones) FindIn(items []Milestone, raw string, byName bool) (Milestone, error) {
	finder := NameOrID[Milestone]{Kind: "milestone", Scope: s.Repository, ID: func(m Milestone) int64 { return m.ID }, Name: func(m Milestone) string { return m.Title }, ByName: byName}
	return finder.Find(items, raw)
}

// CreateUnique creates a milestone unless one has its title, ignoring case, which titles would
// then no longer address. With update it edits that milestone instead and reports true.
func (s Milestones) CreateUnique(ctx context.Context, opts MilestoneOptions, update bool) (Milestone, bool, error) {
	items, err := s.All(ctx)
	if err != nil {
		return Milestone{}, false, err
	}
	finder := NameOrID[Milestone]{Kind: "milestone", Scope: s.Repository, ID: func(m Milestone) int64 { return m.ID }, Name: func(m Milestone) string { return m.Title }, ByName: true}
	current, err := finder.Find(items, deref(opts.Title))
	if errors.As(err, new(*NotFoundError)) {
		milestone, err := s.Create(ctx, opts)
		return milestone, false, err
	}
	if err != nil {
		return Milestone{}, false, err
	}
	if !update {
		return Milestone{}, false, &ConflictError{Reason: ReasonExists, Kind: "milestone", Key: strconv.Quote(current.Title), Scope: s.Repository}
	}
	opts.Title = nil
	milestone, err := s.Edit(ctx, current.ID, opts)
	return milestone, true, err
}

// EditUnique edits current like Edit but refuses to retitle it to the title of another milestone
// among existing, the milestones of the repository, ignoring case, which CreateUnique refuses too:
// titles would no longer address either milestone.
func (s Milestones) EditUnique(ctx context.Context, existing []Milestone, current Milestone, opts MilestoneOptions) (Milestone, error) {
	if opts.Title != nil {
		opts.Title = new(strings.TrimSpace(*opts.Title)) // as Gitea stores it
	}
	if opts.Title != nil && *opts.Title != current.Title {
		for _, other := range existing {
			if other.ID != current.ID && strings.EqualFold(other.Title, *opts.Title) {
				return Milestone{}, &ConflictError{Reason: ReasonExists, Kind: "milestone", Key: strconv.Quote(other.Title), Scope: s.Repository}
			}
		}
	}
	return s.Edit(ctx, current.ID, opts)
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
	finder := NameOrID[Milestone]{Kind: "milestone", Scope: s.Repository, ID: func(m Milestone) int64 { return m.ID }, Name: func(m Milestone) string { return m.Title }}
	result := make([]string, len(values))
	for i, raw := range values {
		item, err := finder.Find(items, raw)
		if err != nil {
			return nil, err
		}
		if result[i], err = s.filterValue(items, item); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// FilterValue is the value that filters an issue listing by item, as FilterIDs resolves it.
func (s Milestones) FilterValue(ctx context.Context, item Milestone) (string, error) {
	items, err := s.All(ctx)
	if err != nil {
		return "", err
	}
	return s.filterValue(items, item)
}

func (s Milestones) filterValue(items []Milestone, item Milestone) (string, error) {
	titles := make(map[string]int, len(items))
	for _, other := range items {
		titles[other.Title]++
	}
	id := strconv.FormatInt(item.ID, 10)
	switch {
	case titles[item.Title] == 1 && !strings.Contains(item.Title, ","):
		return item.Title, nil
	case titles[id] == 0:
		return id, nil
	}
	return "", fmt.Errorf("milestone %q (ID %s) cannot filter issues: another milestone of %s is titled %q", item.Title, id, s.Repository, id)
}
