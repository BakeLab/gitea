// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	gitea "gitea.dev/sdk"
)

// Moved is where Move left an issue: the column Column of the project Project. Unchanged says it
// was there already, so that nothing was sent.
type Moved struct {
	Issue           Issue
	Project, Column string
	Unchanged       bool
}

// Move puts the issue number in a column of the open project board that holds it. project selects
// the board by ID or title, which an issue on several needs; it is otherwise found among the open
// projects the issue is on.
func (s Projects) Move(ctx context.Context, number int64, column, project string) (Moved, error) {
	issue, on, err := Issues{Client: s.Client, Repository: s.Repository}.viewOnProjects(ctx, number)
	if err != nil {
		return Moved{}, err
	}
	refs := make([]string, len(on))
	for i, id := range on {
		refs[i] = strconv.FormatInt(id, 10)
	}
	if project != "" {
		refs = []string{project}
	}
	if len(refs) == 0 {
		return Moved{}, fmt.Errorf("issue #%d is not on any open project", number)
	}
	boards, err := s.Boards(ctx, refs, false)
	if err != nil {
		return Moved{}, err
	}
	if project != "" && !slices.Contains(on, boards[0].ID) {
		return Moved{}, fmt.Errorf("issue #%d is not on project %q", number, boards[0].Title)
	}
	var board *Board
	var target *gitea.ProjectColumn
	for i := range boards {
		found, err := findColumn(boards[i].columns, column)
		if _, missing := errors.AsType[*NotFoundError](err); missing {
			continue
		}
		if err != nil {
			return Moved{}, fmt.Errorf("project %q: %w", boards[i].Title, err)
		}
		if board != nil {
			return Moved{}, fmt.Errorf("column %q is on several projects of issue #%d; select one with --project", column, number)
		}
		board, target = &boards[i], found
	}
	if board == nil {
		available := make([]string, len(boards))
		for i, b := range boards {
			titles := make([]string, len(b.columns))
			for j, c := range b.columns {
				titles[j] = c.Title
			}
			available[i] = b.Title + ": " + strings.Join(titles, ", ")
		}
		return Moved{}, fmt.Errorf("no column matches %q; available columns:\n  %s", column, strings.Join(available, "\n  "))
	}
	if err := s.loadIssues(ctx, board); err != nil {
		return Moved{}, err
	}
	current, held := board.held[issue.ID]
	if !held {
		// Gitea leaves the issues out of the columns of a board that the account cannot change.
		return Moved{}, fmt.Errorf("issue #%d is on project %q, but its columns do not list it; moving it needs write access to the project", number, board.Title)
	}
	moved := Moved{Issue: issue, Project: board.Title, Column: target.Title, Unchanged: current == target.ID}
	if moved.Unchanged {
		return moved, nil
	}
	return moved, do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.MoveProjectIssue(ctx, board.scope, board.ID, issue.ID, gitea.MoveProjectIssueOption{ColumnID: target.ID})
	})
}

// findColumn matches name against columns by ID, then by title, then ignoring case, spaces and
// punctuation, so that "in-progress" finds "In progress".
func findColumn(columns []*gitea.ProjectColumn, name string) (*gitea.ProjectColumn, error) {
	find := NameOrID[*gitea.ProjectColumn]{Kind: "column", ID: func(c *gitea.ProjectColumn) int64 { return c.ID }, Name: func(c *gitea.ProjectColumn) string { return c.Title }}
	column, err := find.Find(columns, name)
	if _, missing := errors.AsType[*NotFoundError](err); !missing {
		return column, err
	}
	want := squash(name)
	if want == "" {
		return nil, err
	}
	var matches []*gitea.ProjectColumn
	for _, c := range columns {
		if squash(c.Title) == want {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 {
		return nil, err
	}
	return matches[0], nil
}

// squash lowers name and drops everything but its letters and digits.
func squash(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}
