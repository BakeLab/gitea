// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package sshkeys

import (
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdSSHKeyAdd represents a sub command of ssh-keys to add an SSH public key
var CmdSSHKeyAdd = cli.Command{
	Name:        "add",
	Usage:       "Add an SSH public key",
	Description: "Add an SSH public key to the current user's profile",
	ArgsUsage:   "<key-file>",
	Action:      RunSSHKeyAdd,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "title",
			Aliases: []string{"t"},
			Usage:   "Title for the key (defaults to the filename without extension)",
		},
	}, flags.LoginOutputFlags...),
}

// RunSSHKeyAdd reads a public key file and registers it with the Gitea instance
func RunSSHKeyAdd(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	if ctx.Args().Len() < 1 {
		return fmt.Errorf("key file path is required")
	}

	keyFile := ctx.Args().First()
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf("could not read key file '%s': %w", keyFile, err)
	}

	keyContent := strings.TrimSpace(string(keyBytes))
	if keyContent == "" {
		return fmt.Errorf("key file '%s' is empty", keyFile)
	}

	title := ctx.String("title")
	if title == "" {
		base := filepath.Base(keyFile)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	key, _, err := ctx.Login.Client().CreatePublicKey(gitea.CreateKeyOption{
		Title: title,
		Key:   keyContent,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Key '%s' (id: %d) added successfully.\n", key.Title, key.ID)
	return nil
}
