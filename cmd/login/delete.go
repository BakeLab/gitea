// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package login

import (
	"context"
	"errors"
	"log"

	"code.gitea.io/tea/modules/config"

	"github.com/urfave/cli/v3"
)

// CmdLoginDelete is a command to delete a login
var CmdLoginDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Remove a Gitea login",
	Description: `Remove a Gitea login`,
	ArgsUsage:   "<login name>",
	Action:      RunLoginDelete,
}

// RunLoginDelete runs the action of a login delete command
func RunLoginDelete(_ context.Context, cmd *cli.Command) error {
	logins, err := config.GetLogins()
	if err != nil {
		log.Fatal(err)
	}

	var name string

	if len(cmd.Args().First()) != 0 {
		name = cmd.Args().First()
	} else if len(logins) == 1 {
		name = logins[0].Name
	} else {
		return errors.New("Please specify a login name")
	}

	return config.DeleteLogin(name)
}
