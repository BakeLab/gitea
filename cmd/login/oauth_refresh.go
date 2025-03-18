// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"fmt"

	"code.gitea.io/tea/modules/auth"
	"code.gitea.io/tea/modules/config"

	"github.com/urfave/cli/v2"
)

// CmdLoginOAuthRefresh represents a command to refresh an OAuth token
var CmdLoginOAuthRefresh = cli.Command{
	Name:        "oauth-refresh",
	Usage:       "Refresh an OAuth token",
	Description: "Manually refresh an expired OAuth token. Usually only used when troubleshooting authentication.",
	ArgsUsage:   "[<login name>]",
	Action:      runLoginOAuthRefresh,
}

func runLoginOAuthRefresh(ctx *cli.Context) error {
	var loginName string

	// Get login name from args or use default
	if ctx.Args().Len() > 0 {
		loginName = ctx.Args().First()
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
	if login.RefreshToken == "" {
		return fmt.Errorf("login '%s' does not have a refresh token. It may have been created using a different authentication method", loginName)
	}

	// Refresh the token
	err := auth.RefreshAccessToken(login)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %s", err)
	}

	fmt.Printf("Successfully refreshed OAuth token for %s\n", loginName)
	return nil
}
