// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	teagit "code.gitea.io/tea/modules/git"
	"github.com/stretchr/testify/assert"
)

func TestRepoFromPath_Worktree(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tea-worktree-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mainRepoPath := filepath.Join(tmpDir, "main-repo")
	worktreePath := filepath.Join(tmpDir, "worktree")

	cmd := exec.Command("git", "init", mainRepoPath)
	assert.NoError(t, cmd.Run())

	cmd = exec.Command("git", "-C", mainRepoPath, "config", "user.email", "test@example.com")
	assert.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", mainRepoPath, "config", "user.name", "Test User")
	assert.NoError(t, cmd.Run())

	cmd = exec.Command("git", "-C", mainRepoPath, "remote", "add", "origin", "https://gitea.com/owner/repo.git")
	assert.NoError(t, cmd.Run())

	readmePath := filepath.Join(mainRepoPath, "README.md")
	err = os.WriteFile(readmePath, []byte("# Test Repo\n"), 0o644)
	assert.NoError(t, err)
	cmd = exec.Command("git", "-C", mainRepoPath, "add", "README.md")
	assert.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", mainRepoPath, "commit", "-m", "Initial commit")
	assert.NoError(t, cmd.Run())

	cmd = exec.Command("git", "-C", mainRepoPath, "worktree", "add", worktreePath, "-b", "test-branch")
	assert.NoError(t, cmd.Run())

	repo, err := teagit.RepoFromPath(worktreePath)
	assert.NoError(t, err, "Should be able to open worktree")

	config, err := repo.Config()
	assert.NoError(t, err, "Should be able to read config")
	assert.NotEmpty(t, config.Remotes, "Should be able to read remotes from worktree")
	assert.Contains(t, config.Remotes, "origin", "Should have origin remote")
	assert.Equal(t, "https://gitea.com/owner/repo.git", config.Remotes["origin"].URLs[0], "Should have correct remote URL")
}
