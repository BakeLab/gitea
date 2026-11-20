// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunnerPolicyMatchingAndValidation(t *testing.T) {
	t.Cleanup(func() { require.NoError(t, SetRunnerPolicies(nil)) })
	policy := RunnerPolicy{Repositories: []string{"BakeLab/private"}, Events: []string{"pull_request"}, AllowedHosts: []string{"github.com"}}
	require.NoError(t, SetRunnerPolicies([]RunnerPolicy{policy}))
	assert.True(t, policy.matches("bakelab/private", "pull_request", "renamed.yml", "arbitrary-job"))
	assert.False(t, policy.matches("Other/private", "pull_request", "renamed.yml", "arbitrary-job"))
	assert.False(t, policy.matches("BakeLab/private", "push", "renamed.yml", "arbitrary-job"))
	policy.Workflows, policy.Jobs = []string{"scan.yml"}, []string{"scan"}
	assert.True(t, policy.matches("BakeLab/private", "pull_request", "scan.yml", "scan"))
	assert.False(t, policy.matches("BakeLab/private", "pull_request", "scan.yml", "publish"))
	for _, host := range []string{"*", "*.github.com", "127.0.0.1", "169.254.169.254", "github.com:443", "https://github.com", "github.com/path"} {
		invalid := policy
		invalid.AllowedHosts = []string{host}
		require.Error(t, SetRunnerPolicies([]RunnerPolicy{invalid}), host)
	}
	isolation := mergeRunnerIsolation(nil, []string{"github.com", "pypi.org"})
	isolation = mergeRunnerIsolation(isolation, []string{"github.com", "example.com"})
	assert.Equal(t, []string{"github.com"}, isolation.AllowedHosts)
	assert.Empty(t, mergeRunnerIsolation(isolation, []string{}).AllowedHosts)
}
