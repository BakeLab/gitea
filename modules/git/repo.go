// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"net/url"

	"github.com/go-git/go-git/v5"
)

// TeaRepo is a go-git Repository, with an extended high level interface.
type TeaRepo struct {
	*git.Repository
}

// RepoForWorkdir tries to open the git repository in the local directory
// for reading or modification.
func RepoForWorkdir() (*TeaRepo, error) {
	return RepoFromPath("")
}

// RepoFromPath tries to open the git repository by path
func RepoFromPath(path string) (*TeaRepo, error) {
	if len(path) == 0 {
		path = "./"
	}
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true, // Enable commondir support for worktrees
	})
	if err != nil {
		return nil, err
	}

	return &TeaRepo{repo}, nil
}

// RemoteURL returns the URL of the given remote
func (r TeaRepo) RemoteURL(remoteName string) (*url.URL, error) {
	remote, err := r.Remote(remoteName)
	if err != nil {
		return nil, err
	}

	return url.Parse(remote.Config().URLs[0])
}
