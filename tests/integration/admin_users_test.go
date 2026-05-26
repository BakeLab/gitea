// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	teacmd "gitea.dev/tea/cmd"
)

func runAdminCommand(t *testing.T, args []string) error {
	t.Helper()

	adminCmd := teacmd.CmdAdmin
	return adminCmd.Run(t.Context(), args)
}

func createAdminTestUser(t *testing.T, client *gitea.Client, username, password string) {
	t.Helper()

	mustChangePassword := false
	user, _, err := client.Admin.CreateUser(t.Context(), gitea.CreateUserOption{
		LoginName:          username,
		Username:           username,
		Email:              username + "@example.com",
		Password:           password,
		MustChangePassword: &mustChangePassword,
	})
	require.NoError(t, err)
	require.Equal(t, username, user.UserName)

	t.Cleanup(func() {
		if _, err := client.Admin.DeleteUser(t.Context(), username); err != nil {
			t.Logf("failed to delete integration test user %q: %v", username, err)
		}
	})
}

func TestAdminUsersCreateRequiresEmail(t *testing.T) {
	login := createIntegrationLogin(t)

	err := runAdminCommand(t, []string{
		"admin", "users", "create",
		"--username", fmt.Sprintf("create-no-email-%d", time.Now().UnixNano()),
		"--password", "secret123",
		"--login", login.Name,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email")
}

func TestAdminUsersCreateAndDelete(t *testing.T) {
	login := createIntegrationLogin(t)
	client := login.Client()
	username := fmt.Sprintf("tea-admin-create-%d", time.Now().UnixNano())

	err := runAdminCommand(t, []string{
		"admin", "users", "create",
		"--username", username,
		"--email", username + "@example.com",
		"--password", "secret123",
		"--admin",
		"--prohibit-login",
		"--visibility", "limited",
		"--login", login.Name,
	})
	require.NoError(t, err)

	createdUser, _, err := client.Users.GetUserInfo(t.Context(), username)
	require.NoError(t, err)
	assert.Equal(t, username, createdUser.UserName)
	assert.Equal(t, username+"@example.com", createdUser.Email)
	assert.True(t, createdUser.IsAdmin)
	assert.True(t, createdUser.ProhibitLogin)
	assert.Equal(t, gitea.VisibleTypeLimited, createdUser.Visibility)

	err = runAdminCommand(t, []string{
		"admin", "users", "delete", username,
		"--confirm",
		"--login", login.Name,
	})
	require.NoError(t, err)

	_, _, err = client.Users.GetUserInfo(t.Context(), username)
	require.Error(t, err)
}

func TestAdminUsersEdit(t *testing.T) {
	login := createIntegrationLogin(t)
	client := login.Client()
	username := fmt.Sprintf("tea-admin-edit-%d", time.Now().UnixNano())
	oldPassword := "old-secret"
	newPassword := "new-secret"
	createAdminTestUser(t, client, username, oldPassword)

	passwordFile := filepath.Join(t.TempDir(), "password.txt")
	require.NoError(t, os.WriteFile(passwordFile, []byte(newPassword+"\n"), 0o600))

	err := runAdminCommand(t, []string{
		"admin", "users", "edit", username,
		"--email", username + "+new@example.com",
		"--full-name", "Tea Integration",
		"--restricted",
		"--password-file", passwordFile,
		"--no-must-change-password",
		"--visibility", "private",
		"--login", login.Name,
	})
	require.NoError(t, err)

	updatedUser, _, err := client.Users.GetUserInfo(t.Context(), username)
	require.NoError(t, err)
	assert.Equal(t, username+"+new@example.com", updatedUser.Email)
	assert.Equal(t, "Tea Integration", updatedUser.FullName)
	assert.True(t, updatedUser.IsActive)
	assert.True(t, updatedUser.Restricted)
	assert.False(t, updatedUser.ProhibitLogin)
	assert.Equal(t, gitea.VisibleTypePrivate, updatedUser.Visibility)

	passwordClient, err := gitea.NewClient(
		integrationGiteaURL,
		gitea.SetBasicAuth(username, newPassword),
		gitea.SetGiteaVersion(""),
	)
	require.NoError(t, err)

	me, _, err := passwordClient.Users.GetMyUserInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, username, me.UserName)
}
