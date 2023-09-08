// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/config"

	"github.com/skratchdot/open-golang/open"
	"github.com/urfave/cli/v2"
)

// CmdLoginEdit represents to login a gitea server.
var CmdLoginEdit = cli.Command{
	Name:        "edit",
	Aliases:     []string{"e"},
	Usage:       "Edit Gitea logins",
	Description: `Edit Gitea logins`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runLoginEdit,
	Flags:       []cli.Flag{&flags.OutputFlag},
}

func runLoginEdit(_ *cli.Context) error {
	return open.Start(config.GetConfigPath())
}
