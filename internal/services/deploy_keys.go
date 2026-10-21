// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// DeployKey is an SSH key that grants access to one repository.
type DeployKey struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	ReadOnly    bool   `json:"read_only"`
	CreatedAt   string `json:"created_at"`
}

func deployKeyFrom(key *gitea.DeployKey) DeployKey {
	return DeployKey{ID: key.ID, Title: key.Title, Key: key.Key, Fingerprint: key.Fingerprint, ReadOnly: key.ReadOnly, CreatedAt: timestamp(key.Created)}
}

// DeployKeys manages the deploy keys of a repository.
type DeployKeys struct {
	Client     *transport.Client
	Repository string
}

// List returns a page of the repository's deploy keys.
func (s DeployKeys) List(ctx context.Context, limit, page int) (Page[DeployKey], error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[DeployKey]{}, err
	}
	keys, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.DeployKey, *gitea.Response, error) {
		return c.Repositories.ListDeployKeys(ctx, owner, repo, gitea.ListDeployKeysOptions{ListOptions: list})
	})
	return convertPage(keys, deployKeyFrom), Classify(err, "repository", s.Repository, "")
}

// View retrieves one deploy key.
func (s DeployKeys) View(ctx context.Context, id int64) (DeployKey, error) {
	key, err := repoConvert(ctx, s.Client, s.Repository, deployKeyFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.DeployKey, *gitea.Response, error) {
		return c.Repositories.GetDeployKey(ctx, owner, repo, id)
	})
	return key, repositoryItemError(ctx, s.Client, s.Repository, err, "deploy key", strconv.FormatInt(id, 10))
}

// Create adds a deploy key, which can push unless readOnly.
func (s DeployKeys) Create(ctx context.Context, title, key string, readOnly bool) (DeployKey, error) {
	created, err := repoConvert(ctx, s.Client, s.Repository, deployKeyFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.DeployKey, *gitea.Response, error) {
		return c.Repositories.CreateDeployKey(ctx, owner, repo, gitea.CreateKeyOption{Title: title, Key: key, ReadOnly: readOnly})
	})
	// A 404 here can only mean the repository is missing or invisible.
	if IsNotFound(err) {
		return created, Classify(err, "repository", s.Repository, "")
	}
	return created, Classify(err, "deploy key", "", s.Repository)
}

// Delete removes one deploy key.
func (s DeployKeys) Delete(ctx context.Context, id int64) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Repositories.DeleteDeployKey(ctx, owner, repo, id)
	})
	return repositoryItemError(ctx, s.Client, s.Repository, err, "deploy key", strconv.FormatInt(id, 10))
}
