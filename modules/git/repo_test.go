// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepoFromPath_Worktree(t *testing.T) {
	// Create a temporary directory for test
	tmpDir, err := os.MkdirTemp("", "tea-worktree-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	mainRepoPath := filepath.Join(tmpDir, "main-repo")
	worktreePath := filepath.Join(tmpDir, "worktree")

	// Initialize main repository
	cmd := exec.Command("git", "init", mainRepoPath)
	assert.NoError(t, cmd.Run())

	// Configure git for the test
	cmd = exec.Command("git", "-C", mainRepoPath, "config", "user.email", "test@example.com")
	assert.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", mainRepoPath, "config", "user.name", "Test User")
	assert.NoError(t, cmd.Run())

	// Add a remote to the main repository
	cmd = exec.Command("git", "-C", mainRepoPath, "remote", "add", "origin", "https://gitea.com/owner/repo.git")
	assert.NoError(t, cmd.Run())

	// Create an initial commit (required for worktree)
	readmePath := filepath.Join(mainRepoPath, "README.md")
	err = os.WriteFile(readmePath, []byte("# Test Repo\n"), 0644)
	assert.NoError(t, err)
	cmd = exec.Command("git", "-C", mainRepoPath, "add", "README.md")
	assert.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", mainRepoPath, "commit", "-m", "Initial commit")
	assert.NoError(t, cmd.Run())

	// Create a worktree
	cmd = exec.Command("git", "-C", mainRepoPath, "worktree", "add", worktreePath, "-b", "test-branch")
	assert.NoError(t, cmd.Run())

	// Test: Open repository from worktree path
	repo, err := RepoFromPath(worktreePath)
	assert.NoError(t, err, "Should be able to open worktree")

	// Test: Read config from worktree (should read from main repo's config)
	config, err := repo.Config()
	assert.NoError(t, err, "Should be able to read config")

	// Verify that remotes are accessible from worktree
	assert.NotEmpty(t, config.Remotes, "Should be able to read remotes from worktree")
	assert.Contains(t, config.Remotes, "origin", "Should have origin remote")
	assert.Equal(t, "https://gitea.com/owner/repo.git", config.Remotes["origin"].URLs[0], "Should have correct remote URL")
}
