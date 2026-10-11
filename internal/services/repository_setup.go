// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
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
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
		s.Client = s.Client.WithTimeout(opts.Timeout)
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
	return repositoryFrom(item), nil
}
