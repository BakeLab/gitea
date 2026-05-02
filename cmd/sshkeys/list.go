// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package sshkeys

import (
	stdctx "context"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"github.com/urfave/cli/v3"
)

// CmdSSHKeyList represents a sub command of ssh-keys to list the current user's SSH keys
var CmdSSHKeyList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List SSH keys",
	Description: "List the SSH keys registered for the current user",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunSSHKeyList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.LoginOutputFlags...),
}

// RunSSHKeyList lists SSH keys for the current user
func RunSSHKeyList(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	client := ctx.Login.Client()

	keys, _, err := client.ListMyPublicKeys(gitea.ListPublicKeysOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.SSHKeysList(keys, ctx.Output)
}
