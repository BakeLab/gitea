// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, out)
	return string(out)
}

func tryGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	_, err := cmd.CombinedOutput()
	return err
}

func TestRepoFromPathSupportsWorktrees(t *testing.T) {
	tmpDir := t.TempDir()
	mainRepoPath := filepath.Join(tmpDir, "main-repo")
	worktreePath := filepath.Join(tmpDir, "worktree")

	runGit(t, "", "init", mainRepoPath)
	runGit(t, mainRepoPath, "config", "user.email", "test@example.com")
	runGit(t, mainRepoPath, "config", "user.name", "Test User")
	runGit(t, mainRepoPath, "remote", "add", "origin", "https://gitea.com/owner/repo.git")

	readmePath := filepath.Join(mainRepoPath, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("# Test Repo\n"), 0o644))
	runGit(t, mainRepoPath, "add", "README.md")
	runGit(t, mainRepoPath, "commit", "-m", "Initial commit")
	runGit(t, mainRepoPath, "worktree", "add", worktreePath, "-b", "test-branch")

	repo, err := RepoFromPath(worktreePath)
	require.NoError(t, err)

	config, err := repo.Config()
	require.NoError(t, err)
	require.Contains(t, config.Remotes, "origin")
	require.Equal(t, "https://gitea.com/owner/repo.git", config.Remotes["origin"].URLs[0])

	head, err := repo.Head()
	require.NoError(t, err)
	require.Equal(t, "test-branch", head.Name().Short())
}

func TestRepoFromPathSupportsSHA256Repos(t *testing.T) {
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "sha256-repo")

	if err := tryGit("", "init", "--object-format=sha256", repoPath); err != nil {
		t.Skip("git does not support sha256 object format in this environment")
	}

	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	runGit(t, repoPath, "remote", "add", "origin", "https://gitea.com/owner/repo.git")

	readmePath := filepath.Join(repoPath, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("sha256\n"), 0o644))
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "Initial commit")

	repo, err := RepoFromPath(repoPath)
	require.NoError(t, err)

	branch, sha, err := repo.TeaGetCurrentBranchNameAndSHA()
	require.NoError(t, err)
	require.NotEmpty(t, branch)
	require.Len(t, sha, 64)

	config, err := repo.Config()
	require.NoError(t, err)
	require.Contains(t, config.Remotes, "origin")
}

func TestTeaFindBranchByShaAndName(t *testing.T) {
	tmpDir := t.TempDir()
	remotePath := filepath.Join(tmpDir, "remote.git")
	localPath := filepath.Join(tmpDir, "local")

	runGit(t, "", "init", "--bare", remotePath)
	runGit(t, "", "clone", remotePath, localPath)
	runGit(t, localPath, "config", "user.email", "test@example.com")
	runGit(t, localPath, "config", "user.name", "Test User")

	filePath := filepath.Join(localPath, "README.md")
	require.NoError(t, os.WriteFile(filePath, []byte("main\n"), 0o644))
	runGit(t, localPath, "add", "README.md")
	runGit(t, localPath, "commit", "-m", "Initial commit")
	runGit(t, localPath, "branch", "-M", "main")
	runGit(t, localPath, "push", "-u", "origin", "main")
	runGit(t, localPath, "checkout", "-b", "feature/demo")

	require.NoError(t, os.WriteFile(filePath, []byte("feature\n"), 0o644))
	runGit(t, localPath, "commit", "-am", "Feature commit")
	runGit(t, localPath, "push", "-u", "origin", "feature/demo")

	repo, err := RepoFromPath(localPath)
	require.NoError(t, err)

	sha := strings.TrimSpace(runGit(t, localPath, "rev-parse", "HEAD"))
	branchBySha, err := repo.TeaFindBranchBySha(sha, remotePath)
	require.NoError(t, err)
	require.NotNil(t, branchBySha)
	require.Equal(t, "feature/demo", branchBySha.Name)
	require.Equal(t, "origin", branchBySha.Remote)

	branchByName, err := repo.TeaFindBranchByName("feature/demo", remotePath)
	require.NoError(t, err)
	require.NotNil(t, branchByName)
	require.Equal(t, "feature/demo", branchByName.Name)

	remote, err := repo.TeaFindBranchRemote("feature/demo", sha)
	require.NoError(t, err)
	require.NotNil(t, remote)
	require.Equal(t, "origin", remote.Config().Name)
}
