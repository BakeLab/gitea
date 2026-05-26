// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package users

import (
	stdctx "context"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"github.com/urfave/cli/v3"
)

var userFieldsFlag = flags.FieldsFlag(print.UserFields, []string{
	"id", "login", "full_name", "email", "activated",
})

// CmdUserList represents a sub command of users to list users
var CmdUserList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List Users",
	Description: "List users",
	Action:      RunUserList,
	Flags: append([]cli.Flag{
		userFieldsFlag,
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunUserList list users
func RunUserList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	fields, err := userFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	users, _, err := client.Admin.ListUsers(requestCtx, gitea.AdminListUsersOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.UserList(users, ctx.Output, fields)
}
