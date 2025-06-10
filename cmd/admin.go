// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/admin/users"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"github.com/urfave/cli/v3"
)

// CmdAdmin represents the namespace of admin commands.
// The command itself has no functionality, but hosts subcommands.
var CmdAdmin = cli.Command{
	Name:     "admin",
	Usage:    "Operations requiring admin access on the Gitea instance",
	Aliases:  []string{"a"},
	Category: catMisc,
	Action: func(_ stdctx.Context, cmd *cli.Command) error {
		return cli.ShowSubcommandHelp(cmd)
	},
	Commands: []*cli.Command{
		&cmdAdminUsers,
	},
}

var cmdAdminUsers = cli.Command{
	Name:    "users",
	Aliases: []string{"u"},
	Usage:   "Manage registered users",
	Action: func(ctx stdctx.Context, cmd *cli.Command) error {
		if cmd.Args().Len() == 1 {
			return runAdminUserDetail(ctx, cmd, cmd.Args().First())
		}
		return users.RunUserList(ctx, cmd)
	},
	Commands: []*cli.Command{
		&users.CmdUserList,
	},
	Flags: users.CmdUserList.Flags,
}

func runAdminUserDetail(_ stdctx.Context, cmd *cli.Command, u string) error {
	ctx := context.InitCommand(cmd)
	client := ctx.Login.Client()
	user, _, err := client.GetUserInfo(u)
	if err != nil {
		return err
	}

	print.UserDetails(user)
	return nil
}
