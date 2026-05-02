// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package sshkeys

import (
	stdctx "context"
	"fmt"
	"strconv"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdSSHKeyDelete represents a sub command of ssh-keys to delete an SSH key by ID
var CmdSSHKeyDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete an SSH key",
	Description: "Delete an SSH key from the current user's profile by its numeric ID",
	ArgsUsage:   "<key-id>",
	Action:      RunSSHKeyDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "Confirm deletion (required)",
		},
	}, flags.LoginOutputFlags...),
}

// RunSSHKeyDelete removes an SSH key by its numeric ID
func RunSSHKeyDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	if ctx.Args().Len() < 1 {
		return fmt.Errorf("key ID is required")
	}

	keyID, err := strconv.ParseInt(ctx.Args().First(), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid key ID '%s': must be a number", ctx.Args().First())
	}

	client := ctx.Login.Client()

	key, resp, err := client.GetPublicKey(keyID)
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return fmt.Errorf("SSH key with ID %d not found", keyID)
		}
		return err
	}

	if !ctx.Bool("confirm") {
		fmt.Printf("Are you sure you want to delete SSH key '%s' (id: %d)? [y/N] ", key.Title, keyID)
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Deletion canceled.")
			return nil
		}
	}

	if _, err = client.DeletePublicKey(keyID); err != nil {
		return err
	}

	fmt.Printf("SSH key '%s' deleted successfully\n", key.Title)
	return nil
}
