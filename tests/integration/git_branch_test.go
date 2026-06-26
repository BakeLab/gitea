// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	teagit "gitea.dev/tea/modules/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeaCheckoutRemoteReferenceKeepsWorktreeClean(t *testing.T) {
	clonePath := setupGitCheckoutTestRepo(t)
	t.Chdir(clonePath)

	repo, err := teagit.RepoFromPath(clonePath)
	require.NoError(t, err)

	err = repo.TeaCheckout(teagit.NewRemoteReferenceName("origin", "feature/test-branch"))
	require.NoError(t, err)

	assert.Empty(t, gitOutput(t, clonePath, "status", "--porcelain"))
	assert.Equal(t, "HEAD", gitOutput(t, clonePath, "rev-parse", "--abbrev-ref", "HEAD"))
}

func TestTeaCreateBranchTracksRemoteBranch(t *testing.T) {
	clonePath := setupGitCheckoutTestRepo(t)
	t.Chdir(clonePath)

	repo, err := teagit.RepoFromPath(clonePath)
	require.NoError(t, err)

	err = repo.TeaCreateBranch("pulls/123", "feature/test-branch", "origin")
	require.NoError(t, err)

	err = repo.TeaCheckout(teagit.NewBranchReferenceName("pulls/123"))
	require.NoError(t, err)

	assert.Empty(t, gitOutput(t, clonePath, "status", "--porcelain"))
	assert.Equal(t, "origin", gitOutput(t, clonePath, "config", "--get", "branch.pulls/123.remote"))
	assert.Equal(t, "refs/heads/feature/test-branch", gitOutput(t, clonePath, "config", "--get", "branch.pulls/123.merge"))
	assert.Equal(t, "pulls/123", gitOutput(t, clonePath, "rev-parse", "--abbrev-ref", "HEAD"))
}

func setupGitCheckoutTestRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	remotePath := filepath.Join(tmpDir, "remote.git")
	seedPath := filepath.Join(tmpDir, "seed")
	clonePath := filepath.Join(tmpDir, "clone")

	runGit(t, tmpDir, "init", "--bare", remotePath)
	runGit(t, tmpDir, "init", seedPath)
	runGit(t, seedPath, "config", "user.email", "test@example.com")
	runGit(t, seedPath, "config", "user.name", "Test User")

	require.NoError(t, os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("# Test Repo\n"), 0o644))
	runGit(t, seedPath, "add", "README.md")
	runGit(t, seedPath, "commit", "-m", "Initial commit")
	runGit(t, seedPath, "branch", "-M", "main")
	runGit(t, seedPath, "remote", "add", "origin", remotePath)
	runGit(t, seedPath, "push", "-u", "origin", "main")

	runGit(t, seedPath, "checkout", "-b", "feature/test-branch")
	require.NoError(t, os.WriteFile(filepath.Join(seedPath, "feature.txt"), []byte("feature\n"), 0o644))
	runGit(t, seedPath, "add", "feature.txt")
	runGit(t, seedPath, "commit", "-m", "Add feature")
	runGit(t, seedPath, "push", "-u", "origin", "feature/test-branch")

	runGit(t, tmpDir, "clone", remotePath, clonePath)
	return clonePath
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	return strings.TrimSpace(string(output))
}
