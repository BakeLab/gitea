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

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Projects places the issues and pull requests of a repository on its project boards, or on
// those of the organization that owns it.
type Projects struct {
	Client     *transport.Client
	Repository string
}

// Board is a project with its default column and the column that holds each issue on it, by the
// issue's global ID.
type Board struct {
	scope gitea.ProjectScope
	*gitea.Project
	defaultColumn int64
	columns       map[int64]int64
}

// Boards resolves the open projects titled titles, a title's case mattering only to tell two
// projects apart: the repository's, else its organization's. It loads each board once, however
// many issues are then put on it or taken off; withIssues also reads which issues each holds,
// which issues that are not new need.
func (s Projects) Boards(ctx context.Context, titles []string, withIssues bool) ([]Board, error) {
	if len(titles) == 0 {
		return nil, nil
	}
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	find := NameOrID[*gitea.Project]{Kind: "project", Scope: s.Repository, ByName: true, NoIDs: true, ID: func(p *gitea.Project) int64 { return p.ID }, Name: func(p *gitea.Project) string { return p.Title }}
	boards := make([]Board, len(titles))
	for _, scope := range []gitea.ProjectScope{gitea.RepoProjectScope(owner, repo), gitea.OrgProjectScope(owner)} {
		if !slices.ContainsFunc(boards, func(b Board) bool { return b.Project == nil }) {
			break
		}
		projects, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Project, *gitea.Response, error) {
			return c.Projects.ListProjects(ctx, scope, gitea.ListProjectsOptions{ListOptions: list})
		})
		// A user rather than an organization owns the repository, or projects are off.
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for i, title := range titles {
			if boards[i].Project != nil {
				continue
			}
			project, err := find.Find(projects, title)
			if _, missing := errors.AsType[*NotFoundError](err); missing {
				continue
			}
			if err != nil {
				return nil, err
			}
			boards[i] = Board{scope: scope, Project: project}
		}
	}
	for i, title := range titles {
		if boards[i].Project == nil {
			return nil, s.missing(ctx, title, owner, repo)
		}
	}
	loaded := map[int64]int{}
	for i := range boards {
		// Two spellings of a title select the same project, which is loaded once.
		if first, ok := loaded[boards[i].ID]; ok {
			boards[i] = boards[first]
			continue
		}
		if err := s.load(ctx, &boards[i], withIssues); err != nil {
			return nil, err
		}
		loaded[boards[i].ID] = i
	}
	return boards, nil
}

// missing reports the open project titled title as not found, or as closed when a closed one has
// the title.
func (s Projects) missing(ctx context.Context, title, owner, repo string) error {
	for _, scope := range []gitea.ProjectScope{gitea.RepoProjectScope(owner, repo), gitea.OrgProjectScope(owner)} {
		closed, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Project, *gitea.Response, error) {
			return c.Projects.ListProjects(ctx, scope, gitea.ListProjectsOptions{ListOptions: list, State: gitea.StateClosed})
		})
		if err != nil && !IsNotFound(err) {
			return err
		}
		if slices.ContainsFunc(closed, func(p *gitea.Project) bool { return strings.EqualFold(p.Title, title) }) {
			return fmt.Errorf("project %q is closed", title)
		}
	}
	return NotFound("project", strconv.Quote(title), s.Repository)
}

// load reads the columns of board and, withIssues, the issues each holds.
func (s Projects) load(ctx context.Context, board *Board, withIssues bool) error {
	columns, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ProjectColumn, *gitea.Response, error) {
		return c.Projects.ListProjectColumns(ctx, board.scope, board.ID, list)
	})
	if err != nil {
		return err
	}
	board.columns = map[int64]int64{}
	if len(columns) == 0 {
		return nil
	}
	board.defaultColumn = columns[max(slices.IndexFunc(columns, func(c *gitea.ProjectColumn) bool { return c.Default }), 0)].ID
	if !withIssues {
		return nil
	}
	held := make([][]*gitea.Issue, len(columns))
	err = concurrently(len(columns), func(i int) error {
		issues, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Projects.ListProjectColumnIssues(ctx, board.scope, board.ID, columns[i].ID, list)
		})
		held[i] = issues
		return err
	})
	for i, issues := range held {
		for _, issue := range issues {
			board.columns[issue.ID] = columns[i].ID
		}
	}
	return err
}

// Add puts the issue with the global ID issueID in the default column of board, and reports
// whether it sent the request: an issue already on the board keeps its column. A board without
// columns first gets the default column Gitea creates for it on the web.
func (s Projects) Add(ctx context.Context, board *Board, issueID int64) (bool, error) {
	if _, held := board.columns[issueID]; held {
		return false, nil
	}
	if board.defaultColumn == 0 {
		if err := s.createDefaultColumn(ctx, board); err != nil {
			return true, err
		}
	}
	return true, do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.AddIssueToProjectColumn(ctx, board.scope, board.ID, board.defaultColumn, issueID)
	})
}

// createDefaultColumn adds the "Uncategorized" default column to board, which has none.
func (s Projects) createDefaultColumn(ctx context.Context, board *Board) error {
	column, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ProjectColumn, *gitea.Response, error) {
		return c.Projects.CreateProjectColumn(ctx, board.scope, board.ID, gitea.CreateProjectColumnOption{Title: "Uncategorized"})
	})
	if err != nil {
		return err
	}
	if err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.SetDefaultProjectColumn(ctx, board.scope, board.ID, column.ID)
	}); err != nil {
		return err
	}
	board.defaultColumn = column.ID
	return nil
}

// Remove takes the issue with the global ID issueID off board, and reports whether it sent the
// request: an issue that is not on it is left as it is.
func (s Projects) Remove(ctx context.Context, board Board, issueID int64) (bool, error) {
	column, held := board.columns[issueID]
	if !held {
		return false, nil
	}
	return true, do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.RemoveIssueFromProjectColumn(ctx, board.scope, board.ID, column, issueID)
	})
}
