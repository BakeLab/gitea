// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"fmt"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/debug"
	"code.gitea.io/tea/modules/git"
)

// contextFromLocalRepo discovers login & repo slug from the default branch remote of the given local repo
func contextFromLocalRepo(repoPath, remoteValue string, extraLogins []config.Login) (*git.TeaRepo, *config.Login, string, error) {
	repo, err := git.RepoFromPath(repoPath)
	if err != nil {
		return nil, nil, "", err
	}
	gitConfig, err := repo.Config()
	if err != nil {
		return repo, nil, "", err
	}
	debug.Printf("Get git config %v of %s in repo %s", gitConfig, remoteValue, repoPath)

	if len(gitConfig.Remotes) == 0 {
		return repo, nil, "", errNotAGiteaRepo
	}

	// When no preferred value is given, choose a remote to find a
	// matching login based on its URL.
	if len(gitConfig.Remotes) > 1 && len(remoteValue) == 0 {
		// if master branch is present, use it as the default remote
		mainBranches := []string{"main", "master", "trunk"}
		for _, b := range mainBranches {
			masterBranch, ok := gitConfig.Branches[b]
			if ok {
				if len(masterBranch.Remote) > 0 {
					remoteValue = masterBranch.Remote
				}
				break
			}
		}
		// if no branch has matched, default to origin or upstream remote.
		if len(remoteValue) == 0 {
			if _, ok := gitConfig.Remotes["upstream"]; ok {
				remoteValue = "upstream"
			} else if _, ok := gitConfig.Remotes["origin"]; ok {
				remoteValue = "origin"
			}
		}
	}
	// make sure a remote is selected
	if len(remoteValue) == 0 {
		for remote := range gitConfig.Remotes {
			remoteValue = remote
			break
		}
	}

	remoteConfig, ok := gitConfig.Remotes[remoteValue]
	if !ok || remoteConfig == nil {
		return repo, nil, "", fmt.Errorf("remote '%s' not found in this Git repository", remoteValue)
	}

	debug.Printf("Get remote configurations %v of %s in repo %s", remoteConfig, remoteValue, repoPath)

	logins, err := config.GetLogins()
	if err != nil {
		return repo, nil, "", err
	}
	// Prepend extra logins (e.g. from env vars) so they are matched first
	if len(extraLogins) > 0 {
		logins = append(extraLogins, logins...)
	}
	for _, u := range remoteConfig.URLs {
		if l, p, err := MatchLogins(u, logins); err == nil {
			return repo, l, p, nil
		}
	}

	return repo, nil, "", errNotAGiteaRepo
}
