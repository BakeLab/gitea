// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// RepositoryTemplateOptions controls copying from a template repository.
type RepositoryTemplateOptions struct {
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch,omitempty"`
	Private       bool   `json:"private"`
	Content       bool   `json:"git_content"`
	GitHooks      bool   `json:"git_hooks"`
	Avatar        bool   `json:"avatar"`
	Labels        bool   `json:"labels"`
	Topics        bool   `json:"topics"`
	Webhooks      bool   `json:"webhooks"`
}

// Generate creates a repository from a template on the selected instance.
func (s Repositories) Generate(ctx context.Context, source string, opts RepositoryTemplateOptions) (Repository, error) {
	if opts.Owner == "" {
		account, err := CurrentAccount(ctx, s.Client)
		if err != nil {
			return Repository{}, err
		}
		opts.Owner = account
	}
	// TODO(sdk): CreateRepoFromTemplateOption has no default_branch.
	var response gitea.Repository
	if err := sendJSON(ctx, s.Client, http.MethodPost, "repos/"+source+"/generate", opts, &response); err != nil {
		return Repository{}, err
	}
	return repositoryFrom(&response), nil
}

// Licenses lists the license templates that repository creation accepts.
func (s Repositories) Licenses(ctx context.Context) ([]string, error) {
	licenses, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.LicensesTemplateListEntry, *gitea.Response, error) {
		return c.Templates.ListLicenses(ctx)
	})
	return convertAll(licenses, func(license *gitea.LicensesTemplateListEntry) string { return license.Key }), err
}

// Gitignores lists the .gitignore templates that repository creation accepts.
func (s Repositories) Gitignores(ctx context.Context) ([]string, error) {
	names, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]string, *gitea.Response, error) {
		return c.Templates.ListGitignores(ctx)
	})
	return names, err
}

// LabelTemplates lists the label sets that repository creation accepts.
func (s Repositories) LabelTemplates(ctx context.Context) ([]string, error) {
	names, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]string, *gitea.Response, error) {
		return c.Templates.ListLabels(ctx)
	})
	return names, err
}

// RepositoryMigrationOptions is explicit server-side import input. Credentials stay in request bodies.
type RepositoryMigrationOptions struct {
	Name           string        `json:"repo_name"`
	Owner          string        `json:"repo_owner,omitempty"`
	CloneURL       string        `json:"clone_addr"`
	Service        string        `json:"service"`
	AuthUser       string        `json:"auth_username,omitempty"`
	AuthPassword   string        `json:"auth_password,omitempty"`
	AuthToken      string        `json:"auth_token,omitempty"`
	Mirror         bool          `json:"mirror"`
	Private        bool          `json:"private"`
	Description    string        `json:"description,omitempty"`
	Wiki           bool          `json:"wiki"`
	Milestones     bool          `json:"milestones"`
	Labels         bool          `json:"labels"`
	Issues         bool          `json:"issues"`
	PullRequests   bool          `json:"pull_requests"`
	Releases       bool          `json:"releases"`
	MirrorInterval string        `json:"mirror_interval,omitempty"`
	LFS            bool          `json:"lfs"`
	LFSEndpoint    string        `json:"lfs_endpoint,omitempty"`
	Timeout        time.Duration `json:"-"`
}

// Migrate imports a remote repository through Gitea's migration API.
func (s Repositories) Migrate(ctx context.Context, opts RepositoryMigrationOptions) (Repository, error) {
	// A migration takes as long as the clone does, unless --timeout bounds it.
	s.Client = s.Client.WithTimeout(opts.Timeout)
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	// TODO(sdk): MigrateRepoOption.Validate counts the description in bytes, but Gitea allows 2048
	// characters; a longer one is set by an edit once the (synchronous) migration is done.
	description := opts.Description
	later := len(description) > sdkDescriptionLimit
	if later {
		opts.Description = ""
	}
	item, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.MigrateRepo(ctx, gitea.MigrateRepoOption{
			RepoName: opts.Name, RepoOwner: opts.Owner, CloneAddr: opts.CloneURL, Service: gitea.GitServiceType(opts.Service),
			AuthUsername: opts.AuthUser, AuthPassword: opts.AuthPassword, AuthToken: opts.AuthToken, Mirror: opts.Mirror,
			Private: opts.Private, Description: opts.Description, Wiki: opts.Wiki, Milestones: opts.Milestones,
			Labels: opts.Labels, Issues: opts.Issues, PullRequests: opts.PullRequests, Releases: opts.Releases,
			MirrorInterval: opts.MirrorInterval, LFS: opts.LFS, LFSEndpoint: opts.LFSEndpoint,
		})
	})
	if err != nil {
		// The server may echo source credentials in its error message.
		if failure, ok := errors.AsType[*transport.APIError](err); ok && (opts.AuthPassword != "" || opts.AuthToken != "") {
			failure.Message = ""
		}
		return Repository{}, err
	}
	migrated := repositoryFrom(item)
	if later {
		edited, err := s.Edit(ctx, migrated.FullName, RepositoryEdit{Description: &description})
		if err != nil {
			return migrated, fmt.Errorf("repository %s was imported, but setting its description failed: %w", migrated.FullName, err)
		}
		return edited, nil
	}
	return migrated, nil
}
