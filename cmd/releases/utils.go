// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package releases

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
)

// GetReleaseByTag finds a release by its tag name.
func GetReleaseByTag(owner, repo, tag string, client *gitea.Client) (*gitea.Release, error) {
	for page := 1; ; {
		rl, resp, err := client.ListReleases(owner, repo, gitea.ListReleasesOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, err
		}
		if page == 1 && len(rl) == 0 {
			return nil, fmt.Errorf("repo does not have any release")
		}
		for _, r := range rl {
			if r.TagName == tag {
				return r, nil
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return nil, fmt.Errorf("release tag does not exist")
}
