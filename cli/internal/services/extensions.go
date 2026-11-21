// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"

	gitea "gitea.dev/sdk"
)

// Commit resolves ref, a branch, tag or commit, to the full commit SHA.
func (s Repositories) Commit(ctx context.Context, repo, ref string) (string, error) {
	commit, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Commit, *gitea.Response, error) {
		return c.Repositories.GetSingleCommit(ctx, owner, name, ref)
	})
	if err != nil {
		return "", repositoryItemError(ctx, s.Client, repo, err, "ref", fmt.Sprintf("%q", ref))
	}
	if commit.CommitMeta == nil || commit.SHA == "" {
		return "", errInvalidJSON
	}
	return commit.SHA, nil
}

// File returns the content of the file at path in the repository at ref, and whether it exists.
func (s Repositories) File(ctx context.Context, repo, ref, path string) ([]byte, bool, error) {
	data, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]byte, *gitea.Response, error) {
		return c.Repositories.GetFile(ctx, owner, name, ref, path)
	})
	if IsNotFound(err) {
		if repoErr := missingRepository(ctx, s.Client, repo); repoErr != nil {
			return nil, false, repoErr
		}
		return nil, false, nil
	}
	return data, err == nil, err
}
