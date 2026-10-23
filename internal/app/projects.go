// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
)

// validateProjectChanges trims the titles of --add-project and --remove-project and rejects blank
// ones and a title named by both, which would flip the project's membership on every run. Titles
// that differ in case may name different projects, which loadProjectChanges tells apart.
func validateProjectChanges(add, remove []string) error {
	if err := requireNames("project", "", add, remove); err != nil {
		return err
	}
	for _, title := range add {
		if slices.Contains(remove, title) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--add-project %q cannot be combined with --remove-project %q", title, title))
		}
	}
	return nil
}

// projectChanges are the project boards that issues or pull requests are put on or taken off,
// resolved once for all of them; projects of the repository come before its owner's.
type projectChanges struct {
	service     services.Projects
	add, remove []services.Board
}

// loadProjectChanges resolves the boards to add to and remove from; existing says whether the
// issues they are applied to may already be on a board, which a new one cannot.
func loadProjectChanges(ctx context.Context, t target, add, remove []string, existing bool) (projectChanges, error) {
	changes := projectChanges{service: services.Projects{Client: t.client, Repository: t.Repo}}
	boards, err := changes.service.Boards(ctx, slices.Concat(add, remove), existing)
	if err != nil {
		return changes, err
	}
	for i, board := range boards[:len(add)] {
		if j := slices.IndexFunc(boards[len(add):], func(b services.Board) bool { return b.ID == board.ID }); j >= 0 {
			return changes, fmt.Errorf("--add-project %q cannot be combined with --remove-project %q, which name the same project", add[i], remove[j])
		}
	}
	changes.add, changes.remove = distinctBoards(boards[:len(add)]), distinctBoards(boards[len(add):])
	return changes, nil
}

// distinctBoards drops repeats of a project, which two spellings of its title select, so that
// each board is changed once.
func distinctBoards(boards []services.Board) []services.Board {
	var distinct []services.Board
	for _, board := range boards {
		if !slices.ContainsFunc(distinct, func(b services.Board) bool { return b.ID == board.ID }) {
			distinct = append(distinct, board)
		}
	}
	return distinct
}

// apply puts the issue with the global ID issueID, as project boards address issues, on the boards
// to add and takes it off the others. It reports whether it sent a request: boards that already
// hold the issue, or lack it, are left as they are.
func (c projectChanges) apply(ctx context.Context, issueID int64) (bool, error) {
	var failures []error
	sent := false
	for i := range c.add {
		added, err := c.service.Add(ctx, &c.add[i], issueID)
		if err != nil {
			failures = append(failures, fmt.Errorf("add to project %q: %w", c.add[i].Title, err))
		}
		sent = sent || added
	}
	for _, board := range c.remove {
		removed, err := c.service.Remove(ctx, board, issueID)
		if err != nil {
			failures = append(failures, fmt.Errorf("remove from project %q: %w", board.Title, err))
		}
		sent = sent || removed
	}
	return sent, errors.Join(failures...)
}

// applyToNumber is apply for the pull request numbered n, whose global ID it looks up.
func (c projectChanges) applyToNumber(ctx context.Context, n int64) (bool, error) {
	if len(c.add)+len(c.remove) == 0 {
		return false, nil
	}
	issue, err := (services.Issues{Client: c.service.Client, Repository: c.service.Repository, Pulls: true}).View(ctx, n)
	if err != nil {
		return false, err
	}
	return c.apply(ctx, issue.ID)
}
