// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package print

import (
	"testing"

	"code.gitea.io/sdk/gitea"
	"github.com/stretchr/testify/assert"
)

func TestPrintableBranchProtectionUsesSeparateWhitelists(t *testing.T) {
	protection := &gitea.BranchProtection{
		EnablePush:                  true,
		ApprovalsWhitelistTeams:     []string{"approve-team"},
		ApprovalsWhitelistUsernames: []string{"approve-user"},
		MergeWhitelistTeams:         []string{"merge-team"},
		MergeWhitelistUsernames:     []string{"merge-user"},
		PushWhitelistTeams:          []string{"push-team"},
		PushWhitelistUsernames:      []string{"push-user"},
	}

	result := printableBranch{
		branch:     &gitea.Branch{Name: "main"},
		protection: protection,
	}.FormatField("protection", false)

	assert.Contains(t, result, "- approving: approve-team/approve-user/")
	assert.Contains(t, result, "- merging: merge-team/merge-user/")
	assert.Contains(t, result, "- pushing: push-team/push-user/")
	assert.NotContains(t, result, "- approving: approve-team/approve-user/merge-team/")
}
