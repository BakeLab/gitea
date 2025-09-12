// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/task"
	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestCreateRepoObjectFormat(t *testing.T) {
	giteaURL := os.Getenv("GITEA_TEA_TEST_URL")
	if giteaURL == "" {
		t.Skip("GITEA_TEA_TEST_URL is not set, skipping test")
	}

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

	giteaUserName := os.Getenv("GITEA_TEA_TEST_USERNAME")
	giteaUserPasword := os.Getenv("GITEA_TEA_TEST_PASSWORD")

	err := task.CreateLogin("test", "", giteaUserName, giteaUserPasword, "", "", "", giteaURL, "", "", true, false, false, false)
	if err != nil && err.Error() != "login name 'test' has already been used" {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reposCmd := &cli.Command{
				Name:     "repos",
				Commands: []*cli.Command{&CmdRepoCreate},
			}
			tt.args = append(tt.args, "--login", "test")
			args := append([]string{"repos", "create"}, tt.args...)

			err := reposCmd.Run(context.Background(), args)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
		})
	}
}
