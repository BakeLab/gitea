// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/task"
	"github.com/stretchr/testify/require"
)

var (
	integrationGiteaURL string
	integrationUsername string
	integrationPassword string
	integrationToken    string
	integrationTokenID  int64
	integrationSetupErr error
	integrationClient   *gitea.Client
)

func TestMain(m *testing.M) {
	integrationGiteaURL = os.Getenv("GITEA_TEA_TEST_URL")
	integrationUsername = os.Getenv("GITEA_TEA_TEST_USERNAME")
	integrationPassword = os.Getenv("GITEA_TEA_TEST_PASSWORD")

	if integrationGiteaURL != "" {
		if integrationUsername == "" || integrationPassword == "" {
			integrationSetupErr = fmt.Errorf("GITEA_TEA_TEST_USERNAME and GITEA_TEA_TEST_PASSWORD are required for integration tests")
		} else {
			integrationClient, integrationSetupErr = gitea.NewClient(
				integrationGiteaURL,
				gitea.SetBasicAuth(integrationUsername, integrationPassword),
				gitea.SetGiteaVersion(""),
			)
			if integrationSetupErr == nil {
				tokenName := fmt.Sprintf("tea-integration-%d", time.Now().UnixNano())
				var token *gitea.AccessToken
				token, _, integrationSetupErr = integrationClient.CreateAccessToken(gitea.CreateAccessTokenOption{
					Name:   tokenName,
					Scopes: []gitea.AccessTokenScope{gitea.AccessTokenScopeAll},
				})
				if integrationSetupErr == nil {
					integrationToken = token.Token
					integrationTokenID = token.ID
				}
			}
		}
	}

	exitCode := m.Run()

	if integrationClient != nil && integrationTokenID != 0 {
		if _, err := integrationClient.DeleteAccessToken(integrationTokenID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to delete integration token %d: %v\n", integrationTokenID, err)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}

	os.Exit(exitCode)
}

func useTempConfigPath(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yml")
	config.SetConfigPathForTesting(configPath)
	config.SetConfigForTesting(config.LocalConfig{})
	t.Cleanup(func() {
		config.SetConfigForTesting(config.LocalConfig{})
		config.SetConfigPathForTesting("")
	})

	return configPath
}

func createIntegrationLogin(t *testing.T) *config.Login {
	t.Helper()

	_ = useTempConfigPath(t)
	if integrationGiteaURL == "" {
		t.Skip("GITEA_TEA_TEST_URL is not set, skipping integration test")
	}
	require.NoError(t, integrationSetupErr)

	require.NotEmpty(t, integrationToken, "integration token setup failed")

	require.NoError(t, task.CreateLogin("integration", integrationToken, "", "", "", "", "", integrationGiteaURL, "", "", true, false, false, false))

	login, err := config.GetLoginByName("integration")
	require.NoError(t, err)
	require.NotNil(t, login)

	return login
}
