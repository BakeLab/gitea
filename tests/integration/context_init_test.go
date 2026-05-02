// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"os"
	"os/exec"
	"testing"

	"code.gitea.io/tea/modules/config"
	teacontext "code.gitea.io/tea/modules/context"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

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

	ctx, err := teacontext.InitCommand(&cliCmd)
	require.NoError(t, err)
	require.Equal(t, "owner", ctx.Owner)
	require.Equal(t, "repo", ctx.Repo)
	require.Equal(t, "owner/repo", ctx.RepoSlug)
	require.Nil(t, ctx.LocalRepo)
	require.NotNil(t, ctx.Login)
	require.Equal(t, "test-login", ctx.Login.Name)
}
