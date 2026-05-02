// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package users

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdUserDelete represents a sub command of users to delete a user
var CmdUserDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm", "remove"},
	Usage:       "Delete a user",
	Description: "Delete a user account",
	ArgsUsage:   "<username>",
	Action:      RunUserDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm deletion without prompting",
		},
	}, flags.AllDefaultFlags...),
}

// RunUserDelete deletes a user
func RunUserDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	if ctx.Args().Len() == 0 {
		return fmt.Errorf("username is required")
	}

	client := ctx.Login.Client()
	username := ctx.Args().First()

	// Get user details first to show what we're deleting
	user, _, err := client.GetUserInfo(username)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	if !ctx.Bool("confirm") {
		userInfo := fmt.Sprintf("%s (ID: %d)", user.UserName, user.ID)
		if user.Email != "" {
			userInfo += fmt.Sprintf(" - %s", user.Email)
		}
		if user.IsAdmin {
			userInfo += " [admin]"
		}
		fmt.Printf("Are you sure you want to delete user %s? [y/N] ", userInfo)
		var response string
		fmt.Scanln(&response)
		if !isConfirmationAccepted(response) {
			fmt.Println("Deletion canceled.")
			return nil
		}
	}

	_, err = client.AdminDeleteUser(username)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	fmt.Printf("User '%s' deleted successfully\n", username)
	return nil
}
