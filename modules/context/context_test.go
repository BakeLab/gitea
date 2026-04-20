// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"os"
	"os/exec"
	"testing"

	"code.gitea.io/tea/modules/config"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
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
		{
			remoteURL:        "git@custom-ssh.example.com:owner/repo.git",
			logins:           []config.Login{{Name: "env", URL: "https://gitea.example.com", SSHHost: "custom-ssh.example.com"}},
			matchedLoginName: "env",
			expectedRepoPath: "owner/repo",
			hasError:         false,
		},
		{
			remoteURL: "https://gitea.example.com/owner/repo.git",
			logins: []config.Login{
				{Name: "env", URL: "https://gitea.example.com"},
				{Name: "config", URL: "https://gitea.example.com"},
			},
			matchedLoginName: "env",
			expectedRepoPath: "owner/repo",
			hasError:         false,
		},
	}

	for _, kase := range kases {
		t.Run(kase.remoteURL, func(t *testing.T) {
			login, repoPath, err := MatchLogins(kase.remoteURL, kase.logins)
			if (err != nil) != kase.hasError {
				t.Errorf("Expected error: %v, got: %v", kase.hasError, err)
			}
			if repoPath != kase.expectedRepoPath {
				t.Errorf("Expected repo path: %s, got: %s", kase.expectedRepoPath, repoPath)
			}
			if !kase.hasError && login.Name != kase.matchedLoginName {
				t.Errorf("Expected login name: %s, got: %s", kase.matchedLoginName, login.Name)
			}
		})
	}
}

func TestInitCommand_WithRepoSlugSkipsLocalRepoDetection(t *testing.T) {
	tmpDir := t.TempDir()
	config.SetConfigForTesting(config.LocalConfig{
		Logins: []config.Login{{
			Name:    "test-login",
			URL:     "https://gitea.example.com",
			Token:   "token",
			User:    "login-user",
			Default: true,
		}},
	})

	cmd := exec.Command("git", "init", "--object-format=sha256", tmpDir)
	cmd.Env = os.Environ()
	require.NoError(t, cmd.Run())

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWd))
	})

	cliCmd := cli.Command{
		Name: "branches",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "login"},
			&cli.StringFlag{Name: "repo"},
			&cli.StringFlag{Name: "remote"},
			&cli.StringFlag{Name: "output"},
		},
	}
	require.NoError(t, cliCmd.Set("repo", "owner/repo"))

	ctx, err := InitCommand(&cliCmd)
	require.NoError(t, err)
	require.Equal(t, "owner", ctx.Owner)
	require.Equal(t, "repo", ctx.Repo)
	require.Equal(t, "owner/repo", ctx.RepoSlug)
	require.Nil(t, ctx.LocalRepo)
	require.NotNil(t, ctx.Login)
	require.Equal(t, "test-login", ctx.Login.Name)
}
