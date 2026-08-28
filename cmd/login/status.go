// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"fmt"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"

	"github.com/urfave/cli/v3"
)

// CmdLoginStatus represents a command to show authentication status for logins.
var CmdLoginStatus = cli.Command{
	Name:        "status",
	Usage:       "Show authentication status for Gitea logins",
	Description: `Verify the stored token for one or all Gitea logins and report its validity.`,
	ArgsUsage:   "[<login name>]",
	Action:      RunLoginStatus,
	Flags:       []cli.Flag{&flags.OutputFlag},
}

// RunLoginStatus verifies one login, or every configured login when no name is
// provided, and prints a short authentication report.
func RunLoginStatus(requestCtx context.Context, cmd *cli.Command) error {
	var logins []config.Login

	switch cmd.Args().Len() {
	case 0:
		var err error
		logins, err = config.GetLogins()
		if err != nil {
			return err
		}
	case 1:
		login, err := config.GetLoginByName(cmd.Args().First())
		if err != nil {
			return err
		}
		if login == nil {
			return fmt.Errorf("login '%s' not found", cmd.Args().First())
		}
		logins = []config.Login{*login}
	default:
		return fmt.Errorf("too many arguments")
	}

	statuses := make([]print.LoginStatus, 0, len(logins))
	for i := range logins {
		statuses = append(statuses, task.CheckLoginStatus(requestCtx, &logins[i]))
	}

	return print.LoginStatuses(statuses, cmd.String("output"))
}
