// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"testing"

	"code.gitea.io/tea/modules/config"
)

func Test_MatchLogins(t *testing.T) {
	kases := []struct {
		remoteURL        string
		logins           []config.Login
		matchedLoginName string
		expectedRepoPath string
		hasError         bool
	}{
		{
			remoteURL:        "https://gitea.com/owner/repo.git",
			logins:           []config.Login{{Name: "gitea.com", URL: "https://gitea.com"}},
			matchedLoginName: "gitea.com",
			expectedRepoPath: "owner/repo",
			hasError:         false,
		},
		{
			remoteURL:        "git@gitea.com:owner/repo.git",
			logins:           []config.Login{{Name: "gitea.com", URL: "https://gitea.com"}},
			matchedLoginName: "gitea.com",
			expectedRepoPath: "owner/repo",
			hasError:         false,
		},
	}

	for _, kase := range kases {
		t.Run(kase.remoteURL, func(t *testing.T) {
			_, repoPath, err := MatchLogins(kase.remoteURL, kase.logins)
			if (err != nil) != kase.hasError {
				t.Errorf("Expected error: %v, got: %v", kase.hasError, err)
			}
			if repoPath != kase.expectedRepoPath {
				t.Errorf("Expected repo path: %s, got: %s", kase.expectedRepoPath, repoPath)
			}
		})
	}
}
