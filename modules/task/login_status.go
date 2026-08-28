// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/print"
)

// CheckLoginStatus verifies the stored token for a login against the server and
// returns a printable status. Unlike config.Login.Client, refresh failures are
// captured in the returned status instead of terminating the process.
func CheckLoginStatus(ctx context.Context, login *config.Login) print.LoginStatus {
	status := print.LoginStatus{
		Name:        login.Name,
		URL:         login.URL,
		AuthMethod:  loginAuthMethod(login),
		TokenExpiry: loginTokenExpiry(login),
		Helper:      HasGitCredentialHelper(*login),
		Default:     login.Default,
	}

	if login.GetAccessToken() == "" {
		status.Error = "Login failed: no access token configured"
		return status
	}

	if err := login.RefreshOAuthTokenIfNeeded(); err != nil {
		status.Error = "Token refresh failed: " + strings.TrimPrefix(err.Error(), "failed to refresh token: ")
		return status
	}

	// A successful refresh updates the token in the secure store, so re-read the
	// expiry for the status line.
	status.TokenExpiry = loginTokenExpiry(login)

	user, _, err := login.ClientWithoutRefresh().Users.GetMyUserInfo(ctx)
	if err != nil {
		status.Error = fmt.Sprintf("Login failed: %s", err)
		return status
	}

	status.Valid = true
	status.User = user.UserName
	return status
}

func loginAuthMethod(login *config.Login) string {
	if login.IsOAuth() {
		return config.AuthMethodOAuth
	}
	return "token"
}

func loginTokenExpiry(login *config.Login) time.Time {
	expiry := login.GetTokenExpiry()
	if expiry.Equal(time.Unix(0, 0)) {
		return time.Time{}
	}
	return expiry
}
