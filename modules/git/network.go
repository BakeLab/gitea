// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

// Clone clones a repository using the active backend.
func Clone(path, remoteURL string, auth *AuthMethod, depth int, insecure bool) (*TeaRepo, error) {
	backend, err := currentBackend().Clone(path, remoteURL, auth, CloneOptions{Depth: depth, Insecure: insecure})
	if err != nil {
		return nil, err
	}
	return newTeaRepo(backend), nil
}

// AddRemote adds a new remote to the repository.
func (r TeaRepo) AddRemote(name, remoteURL string) error {
	return r.backend.AddRemote(name, remoteURL)
}

// SetBranchUpstream configures the branch's upstream remote.
func (r TeaRepo) SetBranchUpstream(branchName, remoteName, remoteBranch string) error {
	return r.backend.SetBranchUpstream(branchName, remoteName, remoteBranch)
}

// Fetch fetches updates from the named remote.
func (r TeaRepo) Fetch(remoteName string, refspecs []string, auth *AuthMethod) error {
	return r.backend.Fetch(remoteName, refspecs, auth)
}
