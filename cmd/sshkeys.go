// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"

	"code.gitea.io/tea/cmd/sshkeys"
	"github.com/urfave/cli/v3"
)

// CmdSSHKeys represents the ssh-keys command group
var CmdSSHKeys = cli.Command{
	Name:        "ssh-keys",
	Aliases:     []string{"ssh-key"},
	Category:    catSetup,
	Usage:       "Manage SSH public keys",
	Description: "List, add, or delete SSH public keys on the current user's account",
	ArgsUsage:   " ",
	Action:      runSSHKeys,
	Commands: []*cli.Command{
		&sshkeys.CmdSSHKeyList,
		&sshkeys.CmdSSHKeyAdd,
		&sshkeys.CmdSSHKeyDelete,
	},
	Flags: sshkeys.CmdSSHKeyList.Flags,
}

func runSSHKeys(ctx stdctx.Context, cmd *cli.Command) error {
	return sshkeys.RunSSHKeyList(ctx, cmd)
}
