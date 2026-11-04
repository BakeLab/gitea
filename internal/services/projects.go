// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Projects places the issues and pull requests of a repository on its project boards, or on
// those of the organization or user that owns it.
type Projects struct {
	Client     *transport.Client
	Repository string
}

// ProjectColumn is a column of a project board.
type ProjectColumn struct {
	ID    int64
	Title string
}

func columnFrom(c *gitea.ProjectColumn) ProjectColumn { return ProjectColumn{ID: c.ID, Title: c.Title} }

// Board is a project with its columns, its default column and the column that holds each issue
// on it, by the issue's global ID.
type Board struct {
	scope gitea.ProjectScope
	*gitea.Project
	columns       []ProjectColumn
	defaultColumn int64
	held          map[int64]int64
}

// Boards resolves the open projects that refs name by ID or title: a number is an ID before it is
// a title, and a title's case matters only to tell two projects apart. Titles are looked up in the
// repository's projects, else its owner's. It loads each board once, however many issues are then
// put on it or taken off; withIssues also reads which issues each holds, which issues that are not
// new need.
func (s Projects) Boards(ctx context.Context, refs []string, withIssues bool) ([]Board, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	var listed []scopedProjects
	next, stop := iter.Pull2(s.scopes(ctx, owner, repo, gitea.StateOpen))
	defer stop()
	// scope lists the projects of the ith scope once; false when there are fewer scopes.
	scope := func(i int) (scopedProjects, bool, error) {
		for len(listed) <= i {
			projects, err, ok := next()
			if !ok || err != nil {
				return scopedProjects{}, false, err
			}
			listed = append(listed, projects)
		}
		return listed[i], true, nil
	}
	find := NameOrID[*gitea.Project]{Kind: "project", Scope: s.Repository, ByName: true, ID: func(p *gitea.Project) int64 { return p.ID }, Name: func(p *gitea.Project) string { return p.Title }}
	resolve := func(ref string) (Board, error) {
		if id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64); err == nil {
			for i := 0; ; i++ {
				projects, ok, err := scope(i)
				if !ok {
					if err != nil {
						return Board{}, err
					}
					break
				}
				if j := slices.IndexFunc(projects.projects, func(p *gitea.Project) bool { return p.ID == id }); j >= 0 {
					return Board{scope: projects.scope, Project: projects.projects[j]}, nil
				}
			}
		}
		for i := 0; ; i++ {
			projects, ok, err := scope(i)
			if !ok {
				if err != nil {
					return Board{}, err
				}
				return Board{}, s.missing(ctx, ref, owner, repo)
			}
			project, err := find.Find(projects.projects, ref)
			if _, missing := errors.AsType[*NotFoundError](err); !missing {
				return Board{scope: projects.scope, Project: project}, err
			}
		}
	}
	boards := make([]Board, len(refs))
	for i, ref := range refs {
		if boards[i], err = resolve(ref); err != nil {
			return nil, err
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

// missing reports the open project that ref names as not found, or as closed when a closed one
// has it as ID or title.
func (s Projects) missing(ctx context.Context, ref, owner, repo string) error {
	id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64)
	numeric := err == nil
	for listed, err := range s.scopes(ctx, owner, repo, gitea.StateClosed) {
		if err != nil {
			return err
		}
		if slices.ContainsFunc(listed.projects, func(p *gitea.Project) bool { return numeric && p.ID == id || strings.EqualFold(p.Title, ref) }) {
			return &ConflictError{Reason: ReasonClosed, Kind: "project", Key: strconv.Quote(ref)}
		}
	}
	return NotFound("project", strconv.Quote(ref), s.Repository)
}

// scopedProjects are the projects of one scope.
type scopedProjects struct {
	scope    gitea.ProjectScope
	projects []*gitea.Project
}

// scopes lists the projects in state that the issues of the repository owner/repo can be put on:
// the repository's, then its owner's. Gitea changes a user's projects only for that user, so
// those of a user who owns the repository count for that user alone.
func (s Projects) scopes(ctx context.Context, owner, repo string, state gitea.StateType) iter.Seq2[scopedProjects, error] {
	return func(yield func(scopedProjects, error) bool) {
		list := func(scope gitea.ProjectScope) (scopedProjects, error) {
			projects, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Project, *gitea.Response, error) {
				return c.Projects.ListProjects(ctx, scope, gitea.ListProjectsOptions{ListOptions: list, State: state})
			})
			return scopedProjects{scope, projects}, err
		}
		listed, err := list(gitea.RepoProjectScope(owner, repo))
		// Projects are off for the repository.
		if !IsNotFound(err) && !yield(listed, err) {
			return
		}
		listed, err = list(gitea.OrgProjectScope(owner))
		if IsNotFound(err) {
			var account string
			if account, err = CurrentAccount(ctx, s.Client); err == nil {
				if !strings.EqualFold(account, owner) {
					return
				}
				listed, err = list(gitea.CurrentUserProjectScope())
			}
		}
		if !IsNotFound(err) {
			yield(listed, err)
		}
	}
}

// load reads the columns of board and, withIssues, the issues each holds.
func (s Projects) load(ctx context.Context, board *Board, withIssues bool) error {
	columns, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ProjectColumn, *gitea.Response, error) {
		return c.Projects.ListProjectColumns(ctx, board.scope, board.ID, list)
	})
	if err != nil {
		return err
	}
	board.columns, board.held = convertAll(columns, columnFrom), map[int64]int64{}
	if len(columns) == 0 {
		return nil
	}
	board.defaultColumn = columns[max(slices.IndexFunc(columns, func(c *gitea.ProjectColumn) bool { return c.Default }), 0)].ID
	if !withIssues {
		return nil
	}
	return s.loadIssues(ctx, board)
}

// loadIssues reads which column of the loaded board holds each issue.
func (s Projects) loadIssues(ctx context.Context, board *Board) error {
	held := make([][]*gitea.Issue, len(board.columns))
	err := concurrently(len(board.columns), func(i int) error {
		issues, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Issue, *gitea.Response, error) {
			return c.Projects.ListProjectColumnIssues(ctx, board.scope, board.ID, board.columns[i].ID, list)
		})
		held[i] = issues
		return err
	})
	for i, issues := range held {
		for _, issue := range issues {
			board.held[issue.ID] = board.columns[i].ID
		}
	}
	return err
}

// Add puts the issue with the global ID issueID in the default column of board, and reports
// whether it sent the request: an issue already on the board keeps its column. A board without
// columns first gets the default column Gitea creates for it on the web.
func (s Projects) Add(ctx context.Context, board *Board, issueID int64) (bool, error) {
	if _, held := board.held[issueID]; held {
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
	column, held := board.held[issueID]
	if !held {
		return false, nil
	}
	return true, do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.RemoveIssueFromProjectColumn(ctx, board.scope, board.ID, column, issueID)
	})
}
