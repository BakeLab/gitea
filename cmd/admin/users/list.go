// Copyright 2021 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package users

import (
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
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
func RunUserList(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)

	fields, err := userFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	users, _, err := client.AdminListUsers(gitea.AdminListUsersOptions{
		ListOptions: ctx.GetListOptions(),
	})
	if err != nil {
		return err
	}

	print.UserList(users, ctx.Output, fields)

	return nil
}
