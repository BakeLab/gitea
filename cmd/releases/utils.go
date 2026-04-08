// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package releases

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// GetReleaseByTag finds a release by its tag name.
func GetReleaseByTag(owner, repo, tag string, client *gitea.Client) (*gitea.Release, error) {
	rl, _, err := client.ListReleases(owner, repo, gitea.ListReleasesOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		return nil, err
	}
	if len(rl) == 0 {
		return nil, fmt.Errorf("repo does not have any release")
	}
	for _, r := range rl {
		if r.TagName == tag {
			return r, nil
		}
	}
	return nil, fmt.Errorf("release tag does not exist")
}
