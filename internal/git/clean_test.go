// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git/gittest"
	"github.com/stretchr/testify/require"
)

type cleanRunner struct {
	calls                                          []string
	dirty, missingLocal, missingDefault, onFeature bool
	branches                                       string
	remoteTracking, config                         map[string]string
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
	case "config":
		if args[1] == "--get-regexp" {
			var lines []string
			for key, value := range r.config {
				if regexp.MustCompile(args[2]).MatchString(key) {
					lines = append(lines, key+" "+value)
				}
			}
			return strings.Join(lines, "\n"), nil
		}
		if value, ok := r.config[args[2]]; ok {
			return value, nil
		}
		return "", &CommandError{ExitCode: 1}
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
	runner := &cleanRunner{branches: "abc123\tpr/7\torigin\ndef456\tother\t\n"}
	branch, remote, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", PreferredNames: []string{"pr/7"}, MatchingRemotes: []string{"origin"},
	})
	require.NoError(t, err)
	require.Equal(t, "pr/7", branch)
	require.Equal(t, "origin", remote)
}

func TestFindCleanBranchIgnoreSHAUsesName(t *testing.T) {
	runner := &cleanRunner{branches: "deadbeef\tfeature\torigin\n"}
	branch, remote, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", IgnoreSHA: true, MatchingRemotes: []string{"origin"},
	})
	require.NoError(t, err)
	require.Equal(t, "feature", branch)
	require.Equal(t, "origin", remote)
}

func TestFindCleanBranchIgnoreSHAPrefersTheBranchRecordedForThePull(t *testing.T) {
	runner := &cleanRunner{
		branches: "aaa111\tfeature\t\nbbb222\tbob/feature\t\n",
		config:   map[string]string{"branch.bob/feature.gitea-cli-pr": "1"},
	}
	options := FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", IgnoreSHA: true, PullNumber: 1, MatchingRemotes: []string{"origin"},
	}
	branch, _, err := FindCleanBranch(context.Background(), runner, options)
	require.NoError(t, err)
	require.Equal(t, "bob/feature", branch)
	// Without the record, OWNER/BRANCH is only preferred by name and the unrelated branch stays a candidate.
	runner.config = nil
	options.PreferredNames = []string{"bob/feature"}
	_, _, err = FindCleanBranch(context.Background(), runner, options)
	require.ErrorContains(t, err, "multiple local branches")
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
	require.Contains(t, runner.calls, "switch --quiet -- main")
	require.Contains(t, runner.calls, "branch -D -- feature")
	require.Contains(t, runner.calls, "-c credential.helper=gt push --delete -- origin refs/heads/feature")
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

func TestFindCleanBranchMatchesTheRemotesThatPullCheckoutAdds(t *testing.T) {
	runner := &cleanRunner{branches: "abc123\tfeature\tpulls/bob/project\n"}
	branch, remote, err := FindCleanBranch(context.Background(), runner, FindCleanBranchOptions{
		Directory: "/work", HeadSHA: "abc123", HeadBranch: "feature", MatchingRemotes: []string{"origin", "pulls/bob/project"},
	})
	require.NoError(t, err)
	require.Equal(t, "feature", branch)
	require.Equal(t, "pulls/bob/project", remote)
}

func TestFindCleanBranchIgnoresATagWithTheSameName(t *testing.T) {
	gittest.Isolate(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "--allow-empty", "--message=x"},
		{"branch", "feature"},
		{"tag", "feature"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	branch, _, err := FindCleanBranch(context.Background(), System{}, FindCleanBranchOptions{
		Directory: dir, HeadBranch: "feature", DefaultBranch: "main", IgnoreSHA: true,
	})
	require.NoError(t, err)
	require.Equal(t, "feature", branch)
}

func TestCleanPullDeletesTheRemoteBranchBesideATagWithTheSameName(t *testing.T) {
	gittest.Isolate(t)
	remote, dir := t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	run(remote, "init", "--quiet", "--bare")
	run(dir, "init", "--quiet", "--initial-branch=main")
	run(dir, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "--quiet", "--allow-empty", "--message=x")
	run(dir, "branch", "feature")
	run(dir, "tag", "feature")
	run(dir, "remote", "add", "origin", remote)
	run(dir, "push", "--quiet", "origin", "refs/heads/main", "refs/heads/feature", "refs/tags/feature")
	result, err := CleanPull(context.Background(), System{}, CleanOptions{
		Directory: dir, DefaultBranch: "main", LocalBranch: "feature", Remote: "origin", RemoteBranch: "feature", DeleteRemote: true,
	})
	require.NoError(t, err)
	require.True(t, result.RemoteDeleted)
	require.Equal(t, "refs/heads/main\nrefs/tags/feature\n", run(remote, "for-each-ref", "--format=%(refname)"))
}
