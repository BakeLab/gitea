// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Repositories implements Gitea repository operations.
type Repositories struct{ Client *transport.Client }

// Repository is the stable command representation of a repository.
type Repository struct {
	ActionsEnabled bool        `json:"actions_enabled"`
	Website        string      `json:"website"`
	ID             int64       `json:"id"`
	Name           string      `json:"name"`
	FullName       string      `json:"full_name"`
	Description    string      `json:"description"`
	URL            string      `json:"url"`
	Private        bool        `json:"private"`
	DefaultBranch  string      `json:"default_branch"`
	CloneURL       string      `json:"clone_url"`
	SSHURL         string      `json:"ssh_url"`
	Archived       bool        `json:"archived"`
	Template       bool        `json:"template"`
	Fork           bool        `json:"fork"`
	Mirror         bool        `json:"mirror"`
	Parent         *Repository `json:"parent,omitempty"`
	// OriginalURL stays out of JSON output: migration sources may embed credentials.
	OriginalURL string `json:"-"`
}

// RepositoryOptions supplies repository creation input.
type RepositoryOptions struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Private       bool   `json:"private"`
	AutoInit      bool   `json:"auto_init"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Labels        string `json:"issue_labels,omitempty"`
	Gitignores    string `json:"gitignores,omitempty"`
	License       string `json:"license,omitempty"`
	Readme        string `json:"readme,omitempty"`
	Template      bool   `json:"template"`
	TrustModel    string `json:"trust_model,omitempty"`
	ObjectFormat  string `json:"object_format_name,omitempty"`
}

// RepositoryEdit distinguishes omitted settings from explicit false values.
type RepositoryEdit struct {
	ActionsEnabled *bool   `json:"has_actions,omitempty"`
	Name           *string `json:"name,omitempty"`
	Website        *string `json:"website,omitempty"`
	DefaultBranch  *string `json:"default_branch,omitempty"`
	Description    *string `json:"description,omitempty"`
	Private        *bool   `json:"private,omitempty"`
	Archived       *bool   `json:"archived,omitempty"`
	Template       *bool   `json:"template,omitempty"`
}

// View retrieves one repository.
func (s Repositories) View(ctx context.Context, repo string) (Repository, error) {
	item, err := s.get(ctx, repo)
	if err != nil {
		return Repository{}, err
	}
	return repositoryFrom(item), nil
}

func (s Repositories) get(ctx context.Context, repo string) (*gitea.Repository, error) {
	return repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.GetRepo(ctx, owner, name)
	})
}

// RepositoryPermissions holds server-side access flags used only for local decisions.
// It is intentionally omitted from the stable Repository JSON schema.
type RepositoryPermissions struct {
	Admin bool `json:"admin"`
	Push  bool `json:"push"`
	Pull  bool `json:"pull"`
}

// viewPermissions reads repository access flags without exposing them on Repository results.
func (s Repositories) viewPermissions(ctx context.Context, repo string) (RepositoryPermissions, error) {
	item, err := s.get(ctx, repo)
	if err != nil || item.Permissions == nil {
		return RepositoryPermissions{}, err
	}
	return RepositoryPermissions{Admin: item.Permissions.Admin, Push: item.Permissions.Push, Pull: item.Permissions.Pull}, nil
}

// assignees lists usernames that can be assigned on repository issues.
func (s Repositories) assignees(ctx context.Context, repo string) ([]string, error) {
	users, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.User, *gitea.Response, error) {
		return c.Repositories.GetAssignees(ctx, owner, name)
	})
	if err != nil {
		return nil, err
	}
	return convertAll(users, login), nil
}

// Create creates a repository for the account or the named organization.
func (s Repositories) Create(ctx context.Context, org string, opts RepositoryOptions) (Repository, error) {
	options := gitea.CreateRepoOption{
		Name: opts.Name, Description: opts.Description, Private: opts.Private, IssueLabels: opts.Labels,
		AutoInit: opts.AutoInit, Template: opts.Template, Gitignores: opts.Gitignores, License: opts.License,
		Readme: opts.Readme, DefaultBranch: opts.DefaultBranch, TrustModel: gitea.TrustModel(opts.TrustModel),
		ObjectFormatName: opts.ObjectFormat,
	}
	item, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Repository, *gitea.Response, error) {
		if org != "" {
			return c.Repositories.CreateOrgRepo(ctx, org, options)
		}
		return c.Repositories.CreateRepo(ctx, options)
	})
	if err != nil {
		return Repository{}, err
	}
	return repositoryFrom(item), nil
}

// Edit updates only explicitly supplied resource fields.
func (s Repositories) Edit(ctx context.Context, repo string, opts RepositoryEdit) (Repository, error) {
	return repoConvert(ctx, s.Client, repo, repositoryFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.EditRepo(ctx, owner, name, gitea.EditRepoOption{
			HasActions: opts.ActionsEnabled, Name: opts.Name, Website: opts.Website, DefaultBranch: opts.DefaultBranch,
			Description: opts.Description, Private: opts.Private, Archived: opts.Archived, Template: opts.Template,
		})
	})
}

// Delete removes the selected repository.
func (s Repositories) Delete(ctx context.Context, repo string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteRepo(ctx, owner, name)
	})
}

// Fork creates a fork for the current user or selected organization.
func (s Repositories) Fork(ctx context.Context, repo, org string) (Repository, error) {
	var options gitea.CreateForkOption
	if org != "" {
		options.Organization = &org
	}
	return repoConvert(ctx, s.Client, repo, repositoryFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.CreateFork(ctx, owner, name, options)
	})
}
