// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"fmt"

	"code.gitea.io/tea/modules/auth"
	"code.gitea.io/tea/modules/config"

	"github.com/urfave/cli/v3"
)

// CmdLoginOAuthRefresh represents a command to refresh an OAuth token
var CmdLoginOAuthRefresh = cli.Command{
	Name:        "oauth-refresh",
	Usage:       "Refresh an OAuth token",
	Description: "Manually refresh an expired OAuth token. If the refresh token is also expired, opens a browser for re-authentication.",
	ArgsUsage:   "[<login name>]",
	Action:      runLoginOAuthRefresh,
}

func runLoginOAuthRefresh(_ context.Context, cmd *cli.Command) error {
	var loginName string

	// Get login name from args or use default
	if cmd.Args().Len() > 0 {
		loginName = cmd.Args().First()
	} else {
		// Get default login
		login, err := config.GetDefaultLogin()
		if err != nil {
			return fmt.Errorf("no login specified and no default login found: %s", err)
		}
		loginName = login.Name
	}

	// Get the login from config
	login := config.GetLoginByName(loginName)
	if login == nil {
		return fmt.Errorf("login '%s' not found", loginName)
	}

	// Check if the login has a refresh token
	if login.GetRefreshToken() == "" {
		return fmt.Errorf("login '%s' does not have a refresh token. It may have been created using a different authentication method", loginName)
	}

	// Try to refresh the token
	err := auth.RefreshAccessToken(login)
	if err == nil {
		fmt.Printf("Successfully refreshed OAuth token for %s\n", loginName)
		return nil
	}

	// Refresh failed - fall back to browser-based re-authentication
	fmt.Printf("Token refresh failed: %s\n", err)
	fmt.Println("Opening browser for re-authentication...")

	if err := auth.ReauthenticateLogin(login); err != nil {
		return fmt.Errorf("re-authentication failed: %s", err)
	}

	fmt.Printf("Successfully re-authenticated %s\n", loginName)
	return nil
}
