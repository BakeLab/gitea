// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"strconv"
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
		// Gitea answers a missing template and a missing owner with the same 404.
		if IsNotFound(err) {
			if missingRepository(ctx, s.Client, source) != nil {
				return Repository{}, &NotFoundError{Kind: "template repository", Key: source, Err: err}
			}
			return Repository{}, &NotFoundError{Kind: "organization", Key: strconv.Quote(opts.Owner), Err: err}
		}
		return Repository{}, Classify(err, "repository", "", "")
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

// RepositoryCreateChoices are what interactive repository creation offers: the organizations of
// the account and the server's license and .gitignore templates.
type RepositoryCreateChoices struct {
	Organizations, Licenses, Gitignores []string
}

// CreateChoices loads the choices of interactive repository creation.
func (s Repositories) CreateChoices(ctx context.Context) (RepositoryCreateChoices, error) {
	var choices RepositoryCreateChoices
	err := loadTogether(
		func() error {
			orgs, err := listEvery(50, func(limit, page int) (Page[Organization], error) {
				return Organizations{Client: s.Client}.List(ctx, true, "", limit, page)
			})
			for _, org := range orgs {
				choices.Organizations = append(choices.Organizations, org.Name)
			}
			return err
		},
		func() (err error) { choices.Licenses, err = s.Licenses(ctx); return err },
		func() (err error) { choices.Gitignores, err = s.Gitignores(ctx); return err },
	)
	return choices, err
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
	// Gitea answers a migration only once the clone is done, which --timeout alone bounds.
	ctx = transport.UnboundedResponse(ctx)
	// The server may echo source credentials in its error message, also within the clone URL;
	// the client redacts them before it cuts the message short.
	ctx = transport.WithSecrets(ctx, opts.AuthPassword, opts.AuthToken)
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	// TODO(sdk): MigrateRepoOption.Validate demands a token for every forge service, though Gitea
	// also imports with a username and password or anonymously, and counts the description in bytes
	// where Gitea allows 2048 characters.
	var item gitea.Repository
	if err := sendJSON(ctx, s.Client, http.MethodPost, "repos/migrate", opts, &item); err != nil {
		return Repository{}, err
	}
	return repositoryFrom(&item), nil
}
