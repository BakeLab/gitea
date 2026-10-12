// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type cleanRunner struct {
	calls                                          []string
	dirty, missingLocal, missingDefault, onFeature bool
	branches                                       string
	remoteTracking                                 map[string]string
}

func (r *cleanRunner) Output(_ context.Context, dir string, prompt bool, args ...string) (string, error) {
	if dir != "/work" {
		return "", &CommandError{ExitCode: 1}
	}
	command := strings.Join(args, " ")
	r.calls = append(r.calls, command)
	switch args[0] {
	case "rev-parse":
		if len(args) > 1 && args[1] == "--is-inside-work-tree" {
			return "true", nil
		}
		if len(args) > 2 && strings.HasPrefix(args[2], "refs/heads/") {
			if r.missingLocal && strings.Contains(args[2], "feature") {
				return "", &CommandError{ExitCode: 1}
			}
			if r.missingDefault && strings.Contains(args[2], "main") {
				return "", &CommandError{ExitCode: 1}
			}
			return "abc123", nil
		}
		if len(args) > 2 && strings.HasPrefix(args[2], "refs/remotes/") {
			if sha, ok := r.remoteTracking[args[2]]; ok {
				return sha, nil
			}
			return "", &CommandError{ExitCode: 1}
		}
	case "status":
		if r.dirty {
			return " M file", nil
		}
	case "symbolic-ref":
		if r.onFeature {
			return "feature", nil
		}
		return "main", nil
	case "for-each-ref":
		return r.branches, nil
	case "check-ref-format", "switch", "branch", "push":
		return "", nil
	}
	return "", nil
}

func TestFindCleanBranchBySHAAndPreferredName(t *testing.T) {
	runner := &cleanRunner{branches: "abc123\tpr/7\torigin/feature\ndef456\tother\t\n"}
	branch, remote, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", PreferredNames: []string{"pr/7"}, MatchingRemotes: []string{"origin"},
	})
	require.NoError(t, err)
	require.Equal(t, "pr/7", branch)
	require.Equal(t, "origin", remote)
}

func TestFindCleanBranchIgnoreSHAUsesName(t *testing.T) {
	runner := &cleanRunner{branches: "deadbeef\tfeature\torigin/feature\n"}
	branch, remote, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", IgnoreSHA: true, MatchingRemotes: []string{"origin"},
	})
	require.NoError(t, err)
	require.Equal(t, "feature", branch)
	require.Equal(t, "origin", remote)
}

func TestFindCleanBranchRejectsAmbiguity(t *testing.T) {
	runner := &cleanRunner{branches: "abc123\tone\t\nabc123\ttwo\t\n"}
	_, _, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", MatchingRemotes: []string{"origin"},
	})
	require.ErrorContains(t, err, "multiple local branches")
}

func TestFindCleanBranchSkipsDefaultBranch(t *testing.T) {
	runner := &cleanRunner{branches: "abc123\tmain\torigin/main\nabc123\tpr/7\t\n"}
	branch, _, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", DefaultBranch: "main", PreferredNames: []string{"pr/7"}, MatchingRemotes: []string{"origin"},
	})
	require.NoError(t, err)
	require.Equal(t, "pr/7", branch)
}

func TestCleanPullRejectsDefaultBranch(t *testing.T) {
	_, err := CleanPull(context.Background(), &cleanRunner{}, CleanOptions{Directory: "/work", DefaultBranch: "main", LocalBranch: "main"})
	require.ErrorContains(t, err, "default branch")
}

func TestCleanPullValidatesBeforeMutation(t *testing.T) {
	runner := &cleanRunner{dirty: true}
	_, err := CleanPull(context.Background(), runner, CleanOptions{Directory: "/work", DefaultBranch: "main", LocalBranch: "feature"})
	require.ErrorContains(t, err, "working tree has changes")
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "switch "))
		require.False(t, strings.HasPrefix(call, "branch "))
		require.False(t, strings.HasPrefix(call, "push "))
	}
}

func TestCleanPullDeletesLocalAndRemoteBranches(t *testing.T) {
	runner := &cleanRunner{onFeature: true}
	result, err := CleanPull(context.Background(), runner, CleanOptions{
		Directory: "/work", DefaultBranch: "main", LocalBranch: "feature", Remote: "origin", RemoteBranch: "feature",
		DeleteRemote: true, CredentialArgs: []string{"-c", "credential.helper=gt"},
	})
	require.NoError(t, err)
	require.Equal(t, CleanResult{LocalBranch: "feature", Remote: "origin", RemoteBranch: "feature", DefaultBranch: "main", RemoteDeleted: true}, result)
	require.Contains(t, runner.calls, "switch -- main")
	require.Contains(t, runner.calls, "branch -D -- feature")
	require.Contains(t, runner.calls, "-c credential.helper=gt push --delete -- origin feature")
}

func TestCleanPullSkipsRemoteWhenUnavailable(t *testing.T) {
	runner := &cleanRunner{}
	result, err := CleanPull(context.Background(), runner, CleanOptions{Directory: "/work", DefaultBranch: "main", LocalBranch: "feature"})
	require.NoError(t, err)
	require.True(t, result.RemoteSkipped)
	require.Equal(t, "feature", result.LocalBranch)
	for _, call := range runner.calls {
		require.False(t, strings.HasPrefix(call, "push "))
	}
}
