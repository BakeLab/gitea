// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/repos"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func useTempConfigPath(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yml")
	config.SetConfigPathForTesting(configPath)
	t.Cleanup(func() {
		config.SetConfigPathForTesting("")
	})

	return configPath
}

func createIntegrationLogin(t *testing.T, giteaURL string) *config.Login {
	t.Helper()

	_ = useTempConfigPath(t)

	username := os.Getenv("GITEA_TEA_TEST_USERNAME")
	password := os.Getenv("GITEA_TEA_TEST_PASSWORD")
	require.NotEmpty(t, username, "GITEA_TEA_TEST_USERNAME is required for integration tests")
	require.NotEmpty(t, password, "GITEA_TEA_TEST_PASSWORD is required for integration tests")

	require.NoError(t, task.CreateLogin("integration", "", username, password, "", "", "", giteaURL, "", "", true, false, false, false))

	login, err := config.GetLoginByName("integration")
	require.NoError(t, err)
	require.NotNil(t, login)

	return login
}

func TestCreateRepoObjectFormat(t *testing.T) {
	giteaURL := os.Getenv("GITEA_TEA_TEST_URL")
	if giteaURL == "" {
		t.Skip("GITEA_TEA_TEST_URL is not set, skipping test")
	}

	login := createIntegrationLogin(t, giteaURL)
	client := login.Client()
	timestamp := time.Now().Unix()

	tests := []struct {
		name        string
		args        []string
		wantOpts    gitea.CreateRepoOption
		wantErr     bool
		errContains string
	}{
		{
			name: "create repo with sha1 object format",
			args: []string{"--name", fmt.Sprintf("test-sha1-%d", timestamp), "--object-format", "sha1"},
			wantOpts: gitea.CreateRepoOption{
				Name:             fmt.Sprintf("test-sha1-%d", timestamp),
				ObjectFormatName: "sha1",
			},
			wantErr: false,
		},
		{
			name: "create repo with sha256 object format",
			args: []string{"--name", fmt.Sprintf("test-sha256-%d", timestamp), "--object-format", "sha256"},
			wantOpts: gitea.CreateRepoOption{
				Name:             fmt.Sprintf("test-sha256-%d", timestamp),
				ObjectFormatName: "sha256",
			},
			wantErr: false,
		},
		{
			name:        "create repo with invalid object format",
			args:        []string{"--name", fmt.Sprintf("test-invalid-%d", timestamp), "--object-format", "invalid"},
			wantErr:     true,
			errContains: "invalid object format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reposCmd := &cli.Command{
				Name:     "repos",
				Commands: []*cli.Command{&repos.CmdRepoCreate},
			}

			args := append([]string{"repos", "create"}, tt.args...)
			args = append(args, "--login", login.Name)

			err := reposCmd.Run(context.Background(), args)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			t.Cleanup(func() {
				if _, delErr := client.DeleteRepo(login.User, tt.wantOpts.Name); delErr != nil {
					t.Logf("failed to delete integration test repo %q: %v", tt.wantOpts.Name, delErr)
				}
			})
		})
	}
}
