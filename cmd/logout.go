// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"code.gitea.io/tea/cmd/login"

	"github.com/urfave/cli/v2"
)

// CmdLogout represents to logout a gitea server.
var CmdLogout = cli.Command{
	Name:        "logout",
	Category:    catSetup,
	Usage:       "Log out from a Gitea server",
	Description: `Log out from a Gitea server`,
	ArgsUsage:   "<login name>",
	Action:      login.RunLoginDelete,
}
