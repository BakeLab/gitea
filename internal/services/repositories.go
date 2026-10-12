// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

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
	Empty          bool        `json:"empty"`
	Stars          int         `json:"stars_count"`
	Forks          int         `json:"forks_count"`
	OpenIssues     int         `json:"open_issues_count"`
	OpenPulls      int         `json:"open_pr_count"`
	Topics         []string    `json:"topics"`
	UpdatedAt      string      `json:"updated_at"`
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
	Issues         *bool   `json:"has_issues,omitempty"`
	Wiki           *bool   `json:"has_wiki,omitempty"`
	PullRequests   *bool   `json:"has_pull_requests,omitempty"`
	Projects       *bool   `json:"has_projects,omitempty"`
	MergeCommits   *bool   `json:"allow_merge_commits,omitempty"`
	Squash         *bool   `json:"allow_squash_merge,omitempty"`
	Rebase         *bool   `json:"allow_rebase,omitempty"`
	MergeStyle     *string `json:"default_merge_style,omitempty"`
	DeleteBranch   *bool   `json:"default_delete_branch_after_merge,omitempty"`
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
	item, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.GetRepo(ctx, owner, name)
	})
	return item, Classify(err, "repository", repo, "")
}

// readmeNames are the README file names that Gitea shows, in order of preference.
var readmeNames = []string{"readme.md", "readme.markdown", "readme", "readme.txt"}

// Readme returns the README in the top directory of the repository at ref: its file name and its
// content, or "" when there is none.
func (s Repositories) Readme(ctx context.Context, repo, ref string) (name, content string, err error) {
	entries, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.ContentsResponse, *gitea.Response, error) {
		return c.Repositories.ListContents(ctx, owner, name, ref, "")
	})
	if err != nil {
		if IsNotFound(err) {
			return "", "", nil
		}
		return "", "", err
	}
	rank := len(readmeNames)
	for _, entry := range entries {
		if i := slices.Index(readmeNames, strings.ToLower(entry.Name)); entry.Type == "file" && i >= 0 && i < rank {
			name, rank = entry.Name, i
		}
	}
	if name == "" {
		return "", "", nil
	}
	data, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]byte, *gitea.Response, error) {
		return c.Repositories.GetFile(ctx, owner, repo, ref, name)
	})
	return name, string(data), err
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

// Assignee is a user that can be assigned on repository issues.
type Assignee struct {
	Username string `json:"username"`
	FullName string `json:"full_name"`
}

// Assignees lists the users that can be assigned on repository issues.
func (s Repositories) Assignees(ctx context.Context, repo string) ([]Assignee, error) {
	users, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.User, *gitea.Response, error) {
		return c.Repositories.GetAssignees(ctx, owner, name)
	})
	return convertAll(users, func(user *gitea.User) Assignee { return Assignee{Username: user.UserName, FullName: user.FullName} }), err
}

// assignees lists usernames that can be assigned on repository issues.
func (s Repositories) assignees(ctx context.Context, repo string) ([]string, error) {
	users, err := s.Assignees(ctx, repo)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(users))
	for i, user := range users {
		names[i] = user.Username
	}
	return names, nil
}

// Tags lists a page of the repository's tag names.
func (s Repositories) Tags(ctx context.Context, repo string, limit, page int) ([]string, error) {
	tags, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Tag, *gitea.Response, error) {
		return c.Repositories.ListRepoTags(ctx, owner, name, gitea.ListRepoTagsOptions{ListOptions: listOptions(limit, page)})
	})
	return convertAll(tags, func(tag *gitea.Tag) string { return tag.Name }), err
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
		return Repository{}, Classify(err, "repository", "", "")
	}
	return repositoryFrom(item), nil
}

// Edit updates only explicitly supplied resource fields.
func (s Repositories) Edit(ctx context.Context, repo string, opts RepositoryEdit) (Repository, error) {
	var mergeStyle *gitea.MergeStyle
	if opts.MergeStyle != nil {
		style := gitea.MergeStyle(*opts.MergeStyle)
		mergeStyle = &style
	}
	item, err := repoConvert(ctx, s.Client, repo, repositoryFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.EditRepo(ctx, owner, name, gitea.EditRepoOption{
			HasActions: opts.ActionsEnabled, Name: opts.Name, Website: opts.Website, DefaultBranch: opts.DefaultBranch,
			Description: opts.Description, Private: opts.Private, Archived: opts.Archived, Template: opts.Template,
			HasIssues: opts.Issues, HasWiki: opts.Wiki, HasPullRequests: opts.PullRequests, HasProjects: opts.Projects,
			AllowMerge: opts.MergeCommits, AllowSquash: opts.Squash, AllowRebase: opts.Rebase, DefaultMergeStyle: mergeStyle,
			DefaultDeleteBranchAfterMerge: opts.DeleteBranch,
		})
	})
	return item, Classify(err, "repository", repo, "")
}

// AddTopic adds a topic to the repository.
func (s Repositories) AddTopic(ctx context.Context, repo, topic string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.AddRepoTopic(ctx, owner, name, topic)
	})
}

// RemoveTopic removes a topic from the repository.
func (s Repositories) RemoveTopic(ctx context.Context, repo, topic string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteRepoTopic(ctx, owner, name, topic)
	})
}

// Delete removes the selected repository.
func (s Repositories) Delete(ctx context.Context, repo string) error {
	err := repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.DeleteRepo(ctx, owner, name)
	})
	return Classify(err, "repository", repo, "")
}

// ForkOptions places a fork: under Organization instead of the account, and named Name instead
// of the forked repository's name.
type ForkOptions struct{ Organization, Name string }

// Fork forks repo for the account or the organization. When that owner already has a fork of
// repo, Fork returns it and reports that it existed.
func (s Repositories) Fork(ctx context.Context, repo string, opts ForkOptions) (Repository, bool, error) {
	var options gitea.CreateForkOption
	if opts.Organization != "" {
		options.Organization = &opts.Organization
	}
	if opts.Name != "" {
		options.Name = &opts.Name
	}
	fork, err := repoConvert(ctx, s.Client, repo, repositoryFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.CreateFork(ctx, owner, name, options)
	})
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok || apiErr.Status != http.StatusConflict || !strings.Contains(apiErr.Message, "already forked") {
		return fork, false, err
	}
	owner := opts.Organization
	if owner == "" {
		if owner, err = CurrentAccount(ctx, s.Client); err != nil {
			return Repository{}, false, err
		}
	}
	forks, listErr := listAll(func(page int) ([]*gitea.Repository, error) {
		return repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Repository, *gitea.Response, error) {
			return c.Repositories.ListForks(ctx, owner, name, gitea.ListForksOptions{ListOptions: listOptions(50, page)})
		})
	})
	if listErr != nil {
		return Repository{}, false, listErr
	}
	for _, existing := range forks {
		if existing.Owner != nil && strings.EqualFold(existing.Owner.UserName, owner) {
			// The fork list omits the parent that a fork's own record has.
			fork, err := s.View(ctx, existing.FullName)
			return fork, err == nil, err
		}
	}
	return Repository{}, false, apiErr
}

// MergeUpstream fast-forwards branch of the fork repo to its parent's branch on the server. It
// returns how Gitea updated the branch: "fast-forward", or "up-to-date" when there was nothing to do.
func (s Repositories) MergeUpstream(ctx context.Context, repo, branch string) (string, error) {
	result, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.MergeUpstreamResponse, *gitea.Response, error) {
		return c.Repositories.MergeUpstream(ctx, owner, name, gitea.MergeUpstreamRequest{Branch: branch, FfOnly: true})
	})
	if err != nil {
		return "", err
	}
	return result.MergeStyle, nil
}

// MirrorSync asks the server to update the pull mirror repo from its source; the update runs in
// the background.
func (s Repositories) MirrorSync(ctx context.Context, repo string) error {
	return repoDo(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.Repositories.MirrorSync(ctx, owner, name)
	})
}
