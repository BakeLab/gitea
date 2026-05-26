// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	stdctx "context"
	"net/url"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
	local_git "gitea.dev/tea/modules/git"
)

// RepoClone creates a local git clone in the given path, and sets up upstream remote
// for fork repos, for good usability with tea.
func RepoClone(
	ctx stdctx.Context,
	path string,
	login *config.Login,
	repoOwner, repoName string,
	callback func(string) (string, error),
	depth int,
) (*local_git.TeaRepo, error) {
	repoMeta, _, err := login.Client().Repositories.GetRepo(ctx, repoOwner, repoName)
	if err != nil {
		return nil, err
	}

	originURL, err := cloneURL(repoMeta, login)
	if err != nil {
		return nil, err
	}

	auth, err := local_git.GetAuthForURL(originURL, login.GetAccessToken(), login.SSHKey, callback)
	if err != nil {
		return nil, err
	}

	// default path behavior as native git
	if path == "" {
		path = repoName
	}

	repo, err := local_git.Clone(path, originURL.String(), auth, depth, login.Insecure)
	if err != nil {
		return nil, err
	}

	// set up upstream remote for forks
	if repoMeta.Fork && repoMeta.Parent != nil {
		upstreamURL, err := cloneURL(repoMeta.Parent, login)
		if err != nil {
			return nil, err
		}
		upstreamBranch := repoMeta.Parent.DefaultBranch
		if err = repo.AddRemote("upstream", upstreamURL.String()); err != nil {
			return nil, err
		}
		if err = repo.SetBranchUpstream(upstreamBranch, "upstream", upstreamBranch); err != nil {
			return nil, err
		}
	}

	return repo, nil
}

func cloneURL(repo *gitea.Repository, login *config.Login) (*url.URL, error) {
	urlStr := repo.CloneURL
	if login.SSHKey != "" {
		urlStr = repo.SSHURL
	}
	return local_git.ParseURL(urlStr)
}
