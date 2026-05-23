// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"
)

// TeaCreateBranch creates a new branch in the repo, tracking from another branch.
func (r TeaRepo) TeaCreateBranch(localBranchName, remoteBranchName, remoteName string) error {
	return r.backend.CreateTrackingBranch(localBranchName, remoteBranchName, remoteName)
}

// TeaCheckout checks out the given branch in the worktree.
func (r TeaRepo) TeaCheckout(ref ReferenceName) error {
	return r.backend.Checkout(ref)
}

// TeaDeleteLocalBranch removes the given branch locally.
func (r TeaRepo) TeaDeleteLocalBranch(branch *Branch) error {
	return r.backend.DeleteLocalBranch(branch.Name)
}

// TeaDeleteRemoteBranch removes the given branch on the given remote via git protocol.
func (r TeaRepo) TeaDeleteRemoteBranch(remoteName, remoteBranch string, auth *AuthMethod) error {
	return r.backend.DeleteRemoteBranch(remoteName, remoteBranch, auth)
}

// TeaFindBranchBySha returns a branch that is at the the given SHA and syncs to the
// given remote repo.
func (r TeaRepo) TeaFindBranchBySha(sha, repoURL string) (b *Branch, err error) {
	remote, err := r.GetRemote(repoURL)
	if err != nil {
		return nil, err
	}
	if remote == nil {
		return nil, fmt.Errorf("no remote found for '%s'", repoURL)
	}
	remoteName := remote.Config().Name

	refs, err := r.backend.ListReferences("refs/heads", "refs/remotes/"+remoteName)
	if err != nil {
		return nil, err
	}

	var remoteRefName ReferenceName
	var localRefName ReferenceName
	for _, ref := range refs {
		if ref.Name().IsRemote() && ref.Hash().String() == sha {
			remoteRefName = ref.Name()
		}
		if ref.Name().IsBranch() && ref.Hash().String() == sha {
			localRefName = ref.Name()
		}
	}
	if remoteRefName == "" || localRefName == "" {
		return nil, nil
	}

	b = &Branch{Remote: remoteName, Name: localRefName.Short(), Merge: localRefName}
	return b, b.Validate()
}

// TeaFindBranchByName returns a branch that is at the the given local and
// remote names and syncs to the given remote repo. This method is less precise
// than TeaFindBranchBySha(), but may be desirable if local and remote branch
// have diverged.
func (r TeaRepo) TeaFindBranchByName(branchName, repoURL string) (b *Branch, err error) {
	remote, err := r.GetRemote(repoURL)
	if err != nil {
		return nil, err
	}
	if remote == nil {
		return nil, fmt.Errorf("no remote found for '%s'", repoURL)
	}
	remoteName := remote.Config().Name

	refs, err := r.backend.ListReferences("refs/heads", "refs/remotes/"+remoteName)
	if err != nil {
		return nil, err
	}

	var remoteRefName ReferenceName
	var localRefName ReferenceName
	remoteSearchingName := fmt.Sprintf("%s/%s", remoteName, branchName)
	for _, ref := range refs {
		if ref.Name().IsRemote() && ref.Name().Short() == remoteSearchingName {
			remoteRefName = ref.Name()
		}
		if ref.Name().IsBranch() && ref.Name().Short() == branchName {
			localRefName = ref.Name()
		}
	}
	if remoteRefName == "" || localRefName == "" {
		return nil, nil
	}

	b = &Branch{Remote: remoteName, Name: localRefName.Short(), Merge: localRefName}
	return b, b.Validate()
}

// TeaFindBranchRemote gives the first remote that has a branch with the same name or sha,
// depending on what is passed in.
// This function is needed, as git does not always define branches in .git/config with remote entries.
// Priority order is: first match of sha and branch -> first match of branch -> first match of sha.
func (r TeaRepo) TeaFindBranchRemote(branchName, hash string) (*Remote, error) {
	remotes, err := r.Remotes()
	if err != nil {
		return nil, err
	}

	switch {
	case len(remotes) == 0:
		return nil, nil
	case len(remotes) == 1:
		return remotes[0], nil
	}

	refs, err := r.backend.ListReferences("refs/remotes")
	if err != nil {
		return nil, err
	}

	remoteByName := make(map[string]*Remote, len(remotes))
	for _, remote := range remotes {
		remoteByName[remote.Config().Name] = remote
	}

	var shaMatch *Remote
	var branchMatch *Remote
	var fullMatch *Remote
	for _, ref := range refs {
		remoteName, remoteBranch, ok := splitRemoteRef(ref.Name())
		if !ok {
			continue
		}
		remote := remoteByName[remoteName]
		if remote == nil {
			continue
		}
		if branchMatch == nil && branchName != "" && branchName == remoteBranch {
			branchMatch = remote
		}
		if shaMatch == nil && hash != "" && hash == ref.Hash().String() {
			shaMatch = remote
		}
		if fullMatch == nil && branchName != "" && branchName == remoteBranch && hash != "" && hash == ref.Hash().String() {
			fullMatch = remote
			break
		}
	}

	switch {
	case fullMatch != nil:
		return fullMatch, nil
	case branchMatch != nil:
		return branchMatch, nil
	case shaMatch != nil:
		return shaMatch, nil
	default:
		return nil, nil
	}
}

// TeaGetCurrentBranchNameAndSHA return the name and sha of the branch witch is currently active.
func (r TeaRepo) TeaGetCurrentBranchNameAndSHA() (string, string, error) {
	localHead, err := r.Head()
	if err != nil {
		return "", "", err
	}

	if !localHead.Name().IsBranch() {
		return "", "", fmt.Errorf("active ref is no branch")
	}

	return localHead.Name().Short(), localHead.Hash().String(), nil
}

// PushToCreatAgitFlowPR pushes the given head to the refs/for/<base>/<topic> ref on the remote to create an agit flow PR.
func (r TeaRepo) PushToCreatAgitFlowPR(remoteName, head, base, topic, title, description string, auth *AuthMethod) error {
	if !strings.HasPrefix(head, "refs/") {
		head = "refs/heads/" + head
	}

	pushOptions := make(map[string]string)
	if len(title) > 0 {
		pushOptions["title"] = b64Encode(title)
	}
	if len(description) > 0 {
		pushOptions["description"] = b64Encode(description)
	}
	return r.backend.PushToAgitFlowPR(remoteName, head, base, topic, pushOptions, auth)
}

func splitRemoteRef(ref ReferenceName) (remoteName, branchName string, ok bool) {
	if !ref.IsRemote() {
		return "", "", false
	}
	parts := strings.SplitN(ref.Short(), "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// b64Encode implements base64 encode for string if necessary.
func b64Encode(s string) string {
	if strings.Contains(s, "\n") || !isASCII(s) {
		return "{base64}" + base64.StdEncoding.EncodeToString([]byte(s))
	}
	return s
}

// isASCII indicates string contains only ASCII.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
}
