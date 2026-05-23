// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"errors"
	"strings"
)

var (
	// ErrRepositoryNotExists indicates the requested path is not inside a git repository.
	ErrRepositoryNotExists = errors.New("repository does not exist")
	// ErrBranchExists indicates the requested branch already exists locally.
	ErrBranchExists = errors.New("branch already exists")
)

// AuthMethod carries backend-agnostic authentication information for git operations.
type AuthMethod struct {
	Scheme        string
	Username      string
	Password      string
	KeyFile       string
	KeyPassphrase string
}

// CloneOptions describes repository clone behavior.
type CloneOptions struct {
	Depth    int
	Insecure bool
}

// RepositoryBackend is the backend abstraction used by TeaRepo.
type RepositoryBackend interface {
	WorkTree() string
	Config() (*Config, error)
	Head() (*Reference, error)
	AddRemote(name, remoteURL string) error
	SetBranchUpstream(branchName, remoteName, remoteBranch string) error
	Fetch(remoteName string, refspecs []string, auth *AuthMethod) error
	CreateTrackingBranch(localBranchName, remoteBranchName, remoteName string) error
	Checkout(ref ReferenceName) error
	DeleteLocalBranch(branchName string) error
	DeleteRemoteBranch(remoteName, remoteBranch string, auth *AuthMethod) error
	ListReferences(prefixes ...string) ([]*Reference, error)
	PushToAgitFlowPR(remoteName, head, base, topic string, pushOptions map[string]string, auth *AuthMethod) error
}

// Backend opens and clones repositories using a concrete git implementation.
type Backend interface {
	Name() string
	Open(path string) (RepositoryBackend, error)
	Clone(path, remoteURL string, auth *AuthMethod, opts CloneOptions) (RepositoryBackend, error)
}

// Config mirrors the repository config fields tea needs.
type Config struct {
	Remotes  map[string]*RemoteConfig
	Branches map[string]*Branch
}

// RemoteConfig stores remote configuration.
type RemoteConfig struct {
	Name string
	URLs []string
}

// Branch stores branch configuration.
type Branch struct {
	Name   string
	Remote string
	Merge  ReferenceName
}

// Validate checks whether the branch contains the fields tea needs.
func (b *Branch) Validate() error {
	if b == nil || b.Name == "" {
		return errors.New("branch name is required")
	}
	return nil
}

// Remote wraps a configured git remote.
type Remote struct {
	repo   *TeaRepo
	config *RemoteConfig
}

// Config returns the remote configuration.
func (r *Remote) Config() *RemoteConfig {
	if r == nil {
		return nil
	}
	return r.config
}

// ReferenceName identifies a git reference.
type ReferenceName string

func (r ReferenceName) String() string { return string(r) }

// Short returns the short display name for the reference.
func (r ReferenceName) Short() string {
	s := string(r)
	switch {
	case strings.HasPrefix(s, "refs/heads/"):
		return strings.TrimPrefix(s, "refs/heads/")
	case strings.HasPrefix(s, "refs/remotes/"):
		return strings.TrimPrefix(s, "refs/remotes/")
	case strings.HasPrefix(s, "refs/tags/"):
		return strings.TrimPrefix(s, "refs/tags/")
	default:
		return s
	}
}

// IsBranch reports whether the reference points to a local branch.
func (r ReferenceName) IsBranch() bool {
	return strings.HasPrefix(string(r), "refs/heads/")
}

// IsRemote reports whether the reference points to a remote-tracking branch.
func (r ReferenceName) IsRemote() bool {
	return strings.HasPrefix(string(r), "refs/remotes/")
}

// IsTag reports whether the reference points to a tag.
func (r ReferenceName) IsTag() bool {
	return strings.HasPrefix(string(r), "refs/tags/")
}

// Hash wraps a git object id.
type Hash string

func (h Hash) String() string { return string(h) }

// Reference stores a resolved git ref and its hash.
type Reference struct {
	name ReferenceName
	hash Hash
}

// Name returns the reference name.
func (r *Reference) Name() ReferenceName { return r.name }

// Hash returns the reference hash.
func (r *Reference) Hash() Hash { return r.hash }

// NewBranchReferenceName constructs a local branch ref name.
func NewBranchReferenceName(name string) ReferenceName {
	if strings.HasPrefix(name, "refs/") {
		return ReferenceName(name)
	}
	return ReferenceName("refs/heads/" + name)
}

// NewRemoteReferenceName constructs a remote-tracking ref name.
func NewRemoteReferenceName(remote, name string) ReferenceName {
	if strings.HasPrefix(name, "refs/") {
		return ReferenceName(name)
	}
	return ReferenceName("refs/remotes/" + remote + "/" + name)
}
