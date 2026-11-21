// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"
	"sync"

	gitea "gitea.dev/sdk"
)

// ProjectWorkflowEvents are the events a project workflow can run on.
var ProjectWorkflowEvents = []string{
	"item_added_to_project", "item_removed_from_project", "item_reopened", "item_closed", "item_column_changed",
	"code_changes_requested", "code_review_approved", "pull_request_merged",
}

// ProjectWorkflowFilters restricts the items a project workflow runs for.
type ProjectWorkflowFilters struct {
	IssueType      string  `json:"issue_type,omitempty"`
	SourceColumnID int64   `json:"source_column_id,omitempty"`
	TargetColumnID int64   `json:"target_column_id,omitempty"`
	LabelIDs       []int64 `json:"label_ids,omitempty"`
}

// ProjectWorkflowActions are what a project workflow does to a matching item.
type ProjectWorkflowActions struct {
	ColumnID       int64   `json:"column_id,omitempty"`
	AddLabelIDs    []int64 `json:"add_label_ids,omitempty"`
	RemoveLabelIDs []int64 `json:"remove_label_ids,omitempty"`
	IssueState     string  `json:"issue_state,omitempty"`
}

// ProjectWorkflow is a stable project workflow result.
type ProjectWorkflow struct {
	ID        int64                  `json:"id"`
	Event     string                 `json:"event"`
	Enabled   bool                   `json:"enabled"`
	Filters   ProjectWorkflowFilters `json:"filters"`
	Actions   ProjectWorkflowActions `json:"actions"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
}

func projectWorkflowFrom(w *gitea.ProjectWorkflow) ProjectWorkflow {
	return ProjectWorkflow{
		ID: w.ID, Event: string(w.Event), Enabled: w.Enabled,
		Filters: ProjectWorkflowFilters(w.Filters), Actions: ProjectWorkflowActions(w.Actions),
		CreatedAt: timestamp(w.Created), UpdatedAt: timestamp(w.Updated),
	}
}

// ProjectWorkflowOptions distinguishes omitted fields: a nil Filters or Actions is left as it is
// on edit. Event applies to creation only.
type ProjectWorkflowOptions struct {
	Event   string
	Enabled *bool
	Filters *ProjectWorkflowFilters
	Actions *ProjectWorkflowActions
}

// workflowSupport memoizes, per client, the *CapabilityError of a server without project
// workflows, or nil once one listed them.
var workflowSupport sync.Map

// Workflows returns every workflow of project.
func (s ProjectBoards) Workflows(ctx context.Context, project Project) ([]ProjectWorkflow, error) {
	scope, err := s.scope()
	if err != nil {
		return nil, err
	}
	items, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ProjectWorkflow, *gitea.Response, error) {
		return c.Projects.ListProjectWorkflows(ctx, scope, project.ID, list)
	})
	switch {
	case IsNotFound(err):
		// The project exists, as every command has found it before.
		unsupported := missingEndpoint("project workflows", "/projects/{id}/workflows")
		unsupported.Reason += "; Gitea has it from 29.0"
		workflowSupport.Store(s.Client, unsupported)
		return nil, unsupported
	case err == nil:
		workflowSupport.Store(s.Client, nil)
	}
	return convertAll(items, projectWorkflowFrom), s.classifyProject(err, project)
}

// workflowError classifies the failure of a request for the workflow id of project, or for its
// workflows when id is 0, telling a 404 of a server without workflows, which it lists once to find
// out, from a missing workflow.
func (s ProjectBoards) workflowError(ctx context.Context, err error, project Project, id int64) error {
	if !IsNotFound(err) {
		return s.classifyProject(err, project)
	}
	support, known := workflowSupport.Load(s.Client)
	if !known {
		_, support = s.Workflows(ctx, project)
	}
	if unsupported, ok := support.(*CapabilityError); ok {
		return unsupported
	}
	if id == 0 {
		return s.classifyProject(err, project)
	}
	return Classify(err, "workflow", strconv.FormatInt(id, 10), s.Scope())
}

// Workflow returns one workflow of project.
func (s ProjectBoards) Workflow(ctx context.Context, project Project, id int64) (ProjectWorkflow, error) {
	scope, err := s.scope()
	if err != nil {
		return ProjectWorkflow{}, err
	}
	item, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ProjectWorkflow, *gitea.Response, error) {
		return c.Projects.GetProjectWorkflow(ctx, scope, project.ID, id)
	})
	if err != nil {
		return ProjectWorkflow{}, s.workflowError(ctx, err, project, id)
	}
	return projectWorkflowFrom(item), nil
}

// CreateWorkflow adds a workflow to project.
func (s ProjectBoards) CreateWorkflow(ctx context.Context, project Project, opts ProjectWorkflowOptions) (ProjectWorkflow, error) {
	scope, err := s.scope()
	if err != nil {
		return ProjectWorkflow{}, err
	}
	create := gitea.CreateProjectWorkflowOption{Event: gitea.ProjectWorkflowEvent(opts.Event), Enabled: opts.Enabled}
	if opts.Filters != nil {
		create.Filters = gitea.ProjectWorkflowFilters(*opts.Filters)
	}
	if opts.Actions != nil {
		create.Actions = gitea.ProjectWorkflowActions(*opts.Actions)
	}
	item, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ProjectWorkflow, *gitea.Response, error) {
		return c.Projects.CreateProjectWorkflow(ctx, scope, project.ID, create)
	})
	if err != nil {
		return ProjectWorkflow{}, s.workflowError(ctx, err, project, 0)
	}
	return projectWorkflowFrom(item), nil
}

// EditWorkflow changes the supplied fields of a workflow of project.
func (s ProjectBoards) EditWorkflow(ctx context.Context, project Project, id int64, opts ProjectWorkflowOptions) (ProjectWorkflow, error) {
	scope, err := s.scope()
	if err != nil {
		return ProjectWorkflow{}, err
	}
	edit := gitea.EditProjectWorkflowOption{Enabled: opts.Enabled}
	if opts.Filters != nil {
		filters := gitea.ProjectWorkflowFilters(*opts.Filters)
		edit.Filters = &filters
	}
	if opts.Actions != nil {
		actions := gitea.ProjectWorkflowActions(*opts.Actions)
		edit.Actions = &actions
	}
	item, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ProjectWorkflow, *gitea.Response, error) {
		return c.Projects.EditProjectWorkflow(ctx, scope, project.ID, id, edit)
	})
	if err != nil {
		return ProjectWorkflow{}, s.workflowError(ctx, err, project, id)
	}
	return projectWorkflowFrom(item), nil
}

// DeleteWorkflow removes a workflow from project.
func (s ProjectBoards) DeleteWorkflow(ctx context.Context, project Project, id int64) error {
	scope, err := s.scope()
	if err != nil {
		return err
	}
	err = do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		return c.Projects.DeleteProjectWorkflow(ctx, scope, project.ID, id)
	})
	return s.workflowError(ctx, err, project, id)
}

// Columns returns the columns of project.
func (s ProjectBoards) Columns(ctx context.Context, project Project) ([]ProjectColumn, error) {
	scope, err := s.scope()
	if err != nil {
		return nil, err
	}
	items, err := callAll(ctx, s.Client, 50, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ProjectColumn, *gitea.Response, error) {
		return c.Projects.ListProjectColumns(ctx, scope, project.ID, list)
	})
	return convertAll(items, columnFrom), s.classifyProject(err, project)
}

// LabelCatalog loads the labels that the workflows of the projects can name: a repository
// project's come from its repository and the organization that owns it, an organization
// project's from the organization.
func (s ProjectBoards) LabelCatalog(ctx context.Context) (IssueLabelCatalog, error) {
	return Labels{Client: s.Client, Repository: s.Repository, Organization: s.Organization}.IssueCatalog(ctx)
}
