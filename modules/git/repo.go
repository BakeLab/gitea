// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"fmt"
	"net/url"
	"sort"
)

// TeaRepo wraps a local git repository behind a swappable backend.
type TeaRepo struct {
	backend RepositoryBackend
}

func newTeaRepo(backend RepositoryBackend) *TeaRepo {
	return &TeaRepo{backend: backend}
}

// RepoForWorkdir tries to open the git repository in the local directory
// for reading or modification.
func RepoForWorkdir() (*TeaRepo, error) {
	return RepoFromPath("")
}

// RepoFromPath tries to open the git repository by path.
func RepoFromPath(path string) (*TeaRepo, error) {
	backend, err := currentBackend().Open(path)
	if err != nil {
		return nil, err
	}
	return newTeaRepo(backend), nil
}

// WorkTree returns the repository work tree path.
func (r TeaRepo) WorkTree() string {
	return r.backend.WorkTree()
}

// Config returns the repository config values tea needs.
func (r TeaRepo) Config() (*Config, error) {
	return r.backend.Config()
}

// Remote returns the configured remote by name.
func (r TeaRepo) Remote(remoteName string) (*Remote, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, err
	}
	remoteCfg, ok := cfg.Remotes[remoteName]
	if !ok {
		return nil, fmt.Errorf("remote %s not found", remoteName)
	}
	return &Remote{repo: &r, config: remoteCfg}, nil
}

// Remotes returns all configured remotes sorted by name.
func (r TeaRepo) Remotes() ([]*Remote, error) {
	cfg, err := r.Config()
	if err != nil {
		return nil, err
	}

	remoteNames := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		remoteNames = append(remoteNames, name)
	}
	sort.Strings(remoteNames)

	remotes := make([]*Remote, 0, len(remoteNames))
	for _, name := range remoteNames {
		remotes = append(remotes, &Remote{repo: &r, config: cfg.Remotes[name]})
	}
	return remotes, nil
}

// Head returns the currently checked out ref.
func (r TeaRepo) Head() (*Reference, error) {
	return r.backend.Head()
}

// RemoteURL returns the URL of the given remote.
func (r TeaRepo) RemoteURL(remoteName string) (*url.URL, error) {
	remote, err := r.Remote(remoteName)
	if err != nil {
		return nil, err
	}
	if len(remote.Config().URLs) == 0 {
		return nil, fmt.Errorf("remote %s has no URL configured", remoteName)
	}
	return ParseURL(remote.Config().URLs[0])
}
