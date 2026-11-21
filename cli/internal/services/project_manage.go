// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Project is a stable project board result.
type Project struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	Type         string `json:"type"`
	CardType     string `json:"card_type"`
	OpenIssues   int64  `json:"open_issues"`
	ClosedIssues int64  `json:"closed_issues"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func projectFrom(project *gitea.Project) Project {
	return Project{
		ID: project.ID, Title: project.Title, Description: project.Description, State: string(project.State),
		Type: string(project.Type), CardType: string(project.CardType), OpenIssues: project.NumOpenIssues,
		ClosedIssues: project.NumClosedIssues, URL: project.HTMLURL, CreatedAt: timestamp(project.Created),
		UpdatedAt: deref(optionalTimestamp(project.Updated)),
	}
}

// ProjectOptions distinguishes omitted project fields during edits. Template applies to new projects only.
type ProjectOptions struct {
	Title       *string
	Description *string
	CardType    *string
	State       *string
	Template    string
}

// ProjectBoards manages the projects of a repository or, with Organization or Account, of an
// organization or of the authenticated user.
type ProjectBoards struct {
	Client                   *transport.Client
	Repository, Organization string
	Account                  bool
}

// Scope names the repository, organization or account the projects belong to.
func (s ProjectBoards) Scope() string {
	switch {
	case s.Account:
		return "your account"
	case s.Organization != "":
		return s.Organization
	}
	return s.Repository
}

func (s ProjectBoards) scope() (gitea.ProjectScope, error) {
	switch {
	case s.Account:
		return gitea.CurrentUserProjectScope(), nil
	case s.Organization != "":
		return gitea.OrgProjectScope(s.Organization), nil
	}
	owner, repo, err := splitRepository(s.Repository)
	return gitea.RepoProjectScope(owner, repo), err
}

func (s ProjectBoards) classify(err error) error {
	if s.Organization != "" {
		return Classify(err, "organization", strconv.Quote(s.Organization), "")
	}
	return Classify(err, "repository", s.Repository, "")
}

// classifyProject classifies a failure of a request about project, which is named by its title
// when known, else by its ID.
func (s ProjectBoards) classifyProject(err error, project Project) error {
	key := strconv.FormatInt(project.ID, 10)
	if project.Title != "" {
		key = strconv.Quote(project.Title)
	}
	err = Classify(err, "project", key, s.Scope())
	if conflict, ok := errors.AsType[*ConflictError](err); ok && conflict.Reason == ReasonClosed {
		conflict.Hint = "reopen it with gt project reopen"
	}
	return err
}

// ListPage returns one page of the projects in state: open, closed or all.
func (s ProjectBoards) ListPage(ctx context.Context, state string, limit, page int) (Page[Project], error) {
	scope, err := s.scope()
	if err != nil {
		return Page[Project]{}, err
	}
	projects, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Project, http.Header, error) {
		projects, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.Project, *gitea.Response, error) {
			return c.Projects.ListProjects(ctx, scope, gitea.ListProjectsOptions{ListOptions: listOptions(limit, page), State: gitea.StateType(state)})
		})
		return projects, headerOf(resp), s.classify(err)
	})
	return convertPage(projects, projectFrom), err
}

// All returns every project in the scope, whatever its state.
func (s ProjectBoards) All(ctx context.Context) ([]Project, error) {
	return listEvery(50, func(limit, page int) (Page[Project], error) { return s.ListPage(ctx, "all", limit, page) })
}

func (s ProjectBoards) finder(byName bool) NameOrID[Project] {
	return NameOrID[Project]{Kind: "project", Scope: s.Scope(), ID: func(p Project) int64 { return p.ID }, Name: func(p Project) string { return p.Title }, ByName: byName}
}

// Find returns the projects that raws address, each by ID or title; byName matches numeric
// values as titles only.
func (s ProjectBoards) Find(ctx context.Context, raws []string, byName bool) ([]Project, error) {
	items, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	return s.finder(byName).FindAll(items, raws)
}

// FindIn returns the project among items, the projects of the scope, that raw addresses like Find.
func (s ProjectBoards) FindIn(items []Project, raw string, byName bool) (Project, error) {
	return s.finder(byName).Find(items, raw)
}

// CreateUnique creates a project unless one among existing, the projects of the scope, has its
// title, ignoring case, which titles would then no longer address. With update it edits that
// project instead and reports true.
func (s ProjectBoards) CreateUnique(ctx context.Context, existing []Project, opts ProjectOptions, update bool) (Project, bool, error) {
	return s.finder(true).CreateUnique(existing, deref(opts.Title), update, func() (Project, error) { return s.Create(ctx, opts) }, func(current Project) (Project, error) {
		opts.Title = nil
		return s.Edit(ctx, current.ID, opts)
	})
}

// EditUnique edits current like Edit but refuses to retitle it to the title of another project
// among existing, ignoring case, which CreateUnique refuses too.
func (s ProjectBoards) EditUnique(ctx context.Context, existing []Project, current Project, opts ProjectOptions) (Project, error) {
	if opts.Title != nil {
		if err := s.finder(true).RefuseRename(existing, current, *opts.Title); err != nil {
			return Project{}, err
		}
	}
	return s.Edit(ctx, current.ID, opts)
}

// Create creates one project.
func (s ProjectBoards) Create(ctx context.Context, opts ProjectOptions) (Project, error) {
	scope, err := s.scope()
	if err != nil {
		return Project{}, err
	}
	project, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Project, *gitea.Response, error) {
		return c.Projects.CreateProject(ctx, scope, gitea.CreateProjectOption{
			Title: *opts.Title, Description: deref(opts.Description), TemplateType: gitea.ProjectTemplateType(opts.Template),
			CardType: gitea.ProjectCardType(deref(opts.CardType)),
		})
	})
	if err != nil {
		return Project{}, s.classify(err)
	}
	return projectFrom(project), nil
}

// Edit updates only explicitly supplied fields.
func (s ProjectBoards) Edit(ctx context.Context, id int64, opts ProjectOptions) (Project, error) {
	scope, err := s.scope()
	if err != nil {
		return Project{}, err
	}
	edit := gitea.EditProjectOption{Title: opts.Title, Description: opts.Description}
	if opts.CardType != nil {
		cardType := gitea.ProjectCardType(*opts.CardType)
		edit.CardType = &cardType
	}
	if opts.State != nil {
		state := gitea.StateType(*opts.State)
		edit.State = &state
	}
	project, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Project, *gitea.Response, error) {
		return c.Projects.EditProject(ctx, scope, id, edit)
	})
	if err != nil {
		return Project{}, s.classifyProject(err, Project{ID: id})
	}
	return projectFrom(project), nil
}

// Delete deletes one project.
func (s ProjectBoards) Delete(ctx context.Context, id int64) error {
	scope, err := s.scope()
	if err != nil {
		return err
	}
	err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.DeleteProject(ctx, scope, id)
	})
	return s.classifyProject(err, Project{ID: id})
}
