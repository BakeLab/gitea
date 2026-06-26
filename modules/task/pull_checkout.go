// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"fmt"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
	local_git "gitea.dev/tea/modules/git"
)

// PullCheckout checkout current workdir to the head branch of specified pull request
func PullCheckout(
	ctx stdctx.Context,
	login *config.Login,
	repoOwner, repoName string,
	forceCreateBranch bool,
	index int64,
	callback func(string) (string, error),
) error {
	client := login.Client()
	pr, _, err := client.PullRequests.GetPullRequest(ctx, repoOwner, repoName, index)
	if err != nil {
		return fmt.Errorf("couldn't fetch PR: %s", err)
	}

	// FIXME: should use ctx.LocalRepo..?
	localRepo, err := local_git.RepoForWorkdir()
	if err != nil {
		return err
	}

	// find or create a matching remote
	remoteURL := remoteURLForPR(login, pr)
	newRemoteName := fmt.Sprintf("pulls/%v", pr.Head.Repository.Owner.UserName)
	// verify related remote is in local repo, otherwise add it
	localRemote, err := localRepo.GetOrCreateRemote(remoteURL, newRemoteName)
	if err != nil {
		return err
	}
	localRemoteName := localRemote.Config().Name

	localRemoteBranchName, err := doPRFetch(login, pr, localRepo, localRemote, callback)
	if err != nil {
		return err
	}

	return doPRCheckout(localRepo, pr, localRemoteName, localRemoteBranchName, remoteURL, forceCreateBranch)
}

func isRemoteDeleted(pr *gitea.PullRequest) bool {
	return pr.Head.Ref == fmt.Sprintf("refs/pull/%d/head", pr.Index)
}

func remoteURLForPR(login *config.Login, pr *gitea.PullRequest) string {
	repo := pr.Head.Repository
	if isRemoteDeleted(pr) {
		repo = pr.Base.Repository
	}
	if len(login.SSHKey) != 0 {
		// login.SSHKey is nonempty, if user specified a key manually or we automatically
		// found a matching private key on this machine during login creation.
		// this means, we are very likely to have a working ssh setup.
		return repo.SSHURL
	}
	return repo.CloneURL
}

func doPRFetch(
	login *config.Login,
	pr *gitea.PullRequest,
	localRepo *local_git.TeaRepo,
	localRemote *local_git.Remote,
	callback func(string) (string, error),
) (string, error) {
	_ = callback
	localRemoteName := localRemote.Config().Name
	localBranchName := pr.Head.Ref
	url, err := localRepo.TeaRemoteURL(localRemoteName)
	if err != nil {
		return "", err
	}
	auth, err := local_git.GetAuthForURL(url, login.GetAccessToken(), login.SSHKey, callback)
	if err != nil {
		return "", err
	}
	refspecs := []string{}
	if isRemoteDeleted(pr) {
		// When the head branch is already deleted, pr.Head.Ref points to
		// `refs/pull/<idx>/head`, where the commits stay available.
		// This ref must be fetched explicitly, and does not allow pushing, so we use it
		// only in this case as fallback.
		localBranchName = fmt.Sprintf("pulls/%d", pr.Index)
		refspecs = []string{fmt.Sprintf("%s:refs/remotes/%s/%s",
			pr.Head.Ref,
			localRemoteName,
			localBranchName,
		)}
	}
	fmt.Printf("Fetching PR %v (head %s:%s) from remote '%s'\n", pr.Index, url, pr.Head.Ref, localRemoteName)

	err = localRepo.Fetch(localRemoteName, refspecs, auth)
	if err != nil {
		return "", err
	}
	return localBranchName, nil
}

func doPRCheckout(
	localRepo *local_git.TeaRepo,
	pr *gitea.PullRequest,
	localRemoteName,
	localRemoteBranchName,
	remoteURL string,
	forceCreateBranch bool,
) error {
	// determine the ref to checkout, depending on existence of a matching commit on a local branch
	var info string
	var checkoutRef local_git.ReferenceName

	if b, _ := localRepo.TeaFindBranchBySha(pr.Head.Sha, remoteURL); b != nil {

		// if a matching branch exists, use that
		checkoutRef = local_git.NewBranchReferenceName(b.Name)
		info = fmt.Sprintf("Found matching local branch %s, checking it out", checkoutRef.Short())

	} else if forceCreateBranch {

		// create a branch if wanted
		localBranchName := fmt.Sprintf("pulls/%v", pr.Index)
		if isRemoteDeleted(pr) {
			localBranchName += "-" + pr.Head.Ref
		}
		checkoutRef = local_git.NewBranchReferenceName(localBranchName)
		if err := localRepo.TeaCreateBranch(localBranchName, localRemoteBranchName, localRemoteName); err == nil {
			info = fmt.Sprintf("Created branch '%s'\n", localBranchName)
		} else if err == local_git.ErrBranchExists {
			info = "There may be changes since you last checked out, run `git pull` to get them."
		} else {
			return err
		}

	} else {

		// use the remote tracking branch
		checkoutRef = local_git.NewRemoteReferenceName(localRemoteName, localRemoteBranchName)
		info = fmt.Sprintf(
			"Checking out remote tracking branch %s. To make changes, create a new branch:\n  git checkout %s",
			checkoutRef.String(), localRemoteBranchName)

	}

	fmt.Println(info)
	return localRepo.TeaCheckout(checkoutRef)
}
