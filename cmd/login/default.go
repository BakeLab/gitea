// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/config"

	"github.com/urfave/cli/v3"
)

// CmdLoginSetDefault represents to login a gitea server.
var CmdLoginSetDefault = cli.Command{
	Name:        "default",
	Usage:       "Get or Set Default Login",
	Description: `Get or Set Default Login`,
	ArgsUsage:   "<Login>",
	Action:      runLoginSetDefault,
	Flags:       []cli.Flag{&flags.OutputFlag},
}

func runLoginSetDefault(_ context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		l, err := config.GetDefaultLogin()
		if err != nil {
			return err
		}
		fmt.Printf("Default Login: %s\n", l.Name)
		return nil
	}

	name := cmd.Args().First()
	return config.SetDefaultLogin(name)
}
