// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"code.gitea.io/tea/cmd/admin/users"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"github.com/urfave/cli/v2"
)

// CmdAdmin represents the namespace of admin commands.
// The command itself has no functionality, but hosts subcommands.
var CmdAdmin = cli.Command{
	Name:     "admin",
	Usage:    "Operations requiring admin access on the Gitea instance",
	Aliases:  []string{"a"},
	Category: catMisc,
	Action: func(cmd *cli.Context) error {
		return cli.ShowSubcommandHelp(cmd)
	},
	Subcommands: []*cli.Command{
		&cmdAdminUsers,
	},
}

var cmdAdminUsers = cli.Command{
	Name:    "users",
	Aliases: []string{"u"},
	Usage:   "Manage registered users",
	Action: func(ctx *cli.Context) error {
		if ctx.Args().Len() == 1 {
			return runAdminUserDetail(ctx, ctx.Args().First())
		}
		return users.RunUserList(ctx)
	},
	Subcommands: []*cli.Command{
		&users.CmdUserList,
	},
	Flags: users.CmdUserList.Flags,
}

func runAdminUserDetail(cmd *cli.Context, u string) error {
	ctx := context.InitCommand(cmd)
	client := ctx.Login.Client()
	user, _, err := client.GetUserInfo(u)
	if err != nil {
		return err
	}

	print.UserDetails(user)
	return nil
}
