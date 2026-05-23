// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeBackend struct{}

type fakeRepoBackend struct {
	workTree string
}

func (fakeBackend) Name() string { return "fake" }

func (fakeBackend) Open(path string) (RepositoryBackend, error) {
	return &fakeRepoBackend{workTree: "open:" + path}, nil
}

func (fakeBackend) Clone(path, remoteURL string, auth *AuthMethod, opts CloneOptions) (RepositoryBackend, error) {
	return &fakeRepoBackend{workTree: fmt.Sprintf("clone:%s:%s", path, remoteURL)}, nil
}

func (r *fakeRepoBackend) WorkTree() string { return r.workTree }
func (r *fakeRepoBackend) Config() (*Config, error) {
	return &Config{Remotes: map[string]*RemoteConfig{}, Branches: map[string]*Branch{}}, nil
}

func (r *fakeRepoBackend) Head() (*Reference, error) {
	return &Reference{name: NewBranchReferenceName("main"), hash: Hash("deadbeef")}, nil
}
func (r *fakeRepoBackend) AddRemote(name, remoteURL string) error { return nil }
func (r *fakeRepoBackend) SetBranchUpstream(branchName, remoteName, remoteBranch string) error {
	return nil
}

func (r *fakeRepoBackend) Fetch(remoteName string, refspecs []string, auth *AuthMethod) error {
	return nil
}

func (r *fakeRepoBackend) CreateTrackingBranch(localBranchName, remoteBranchName, remoteName string) error {
	return nil
}
func (r *fakeRepoBackend) Checkout(ref ReferenceName) error          { return nil }
func (r *fakeRepoBackend) DeleteLocalBranch(branchName string) error { return nil }
func (r *fakeRepoBackend) DeleteRemoteBranch(remoteName, remoteBranch string, auth *AuthMethod) error {
	return nil
}
func (r *fakeRepoBackend) ListReferences(prefixes ...string) ([]*Reference, error) { return nil, nil }
func (r *fakeRepoBackend) PushToAgitFlowPR(remoteName, head, base, topic string, pushOptions map[string]string, auth *AuthMethod) error {
	return nil
}

func TestCanSwitchBackends(t *testing.T) {
	setBackendForTesting(t, fakeBackend{})

	repo, err := RepoFromPath("demo")
	require.NoError(t, err)
	require.Equal(t, "open:demo", repo.WorkTree())
	require.Equal(t, "fake", CurrentBackendName())

	cloned, err := Clone("target", "https://example.com/repo.git", nil, 1, false)
	require.NoError(t, err)
	require.Equal(t, "clone:target:https://example.com/repo.git", cloned.WorkTree())
}

func TestRegisteredBackendsContainsCLI(t *testing.T) {
	require.Contains(t, RegisteredBackends(), "cli")
}
