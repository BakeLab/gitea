// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package users

import (
	stdctx "context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// CmdUserCreate represents a sub command of users to create a user
var CmdUserCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"add", "new"},
	Usage:       "Create a new user",
	Description: "Create a new user account",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunUserCreate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:     "username",
			Aliases:  []string{"u"},
			Usage:    "Username for the new user (required)",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "password",
			Aliases: []string{"p"},
			Usage:   "Password for the new user (will prompt if not provided)",
		},
		&cli.StringFlag{
			Name:  "password-file",
			Usage: "Read password from file",
		},
		&cli.BoolFlag{
			Name:  "password-stdin",
			Usage: "Read password from stdin",
		},
		&cli.StringFlag{
			Name:     "email",
			Aliases:  []string{"e"},
			Usage:    "Email address for the new user (required)",
			Required: true,
		},
		&cli.StringFlag{
			Name:  "full-name",
			Usage: "Full name for the new user",
		},
		&cli.BoolFlag{
			Name:  "admin",
			Usage: "Make the user an administrator",
		},
		&cli.BoolFlag{
			Name:  "restricted",
			Usage: "Make the user restricted",
		},
		&cli.BoolFlag{
			Name:  "prohibit-login",
			Usage: "Prohibit the user from logging in",
		},
		&cli.BoolFlag{
			Name:  "no-must-change-password",
			Usage: "Don't require the user to change password on first login (default: password change required)",
		},
		&cli.StringFlag{
			Name:  "visibility",
			Usage: "Visibility of the user profile (public, limited, private)",
			Value: "public",
		},
	}, flags.AllDefaultFlags...),
}

// RunUserCreate creates a new user
func RunUserCreate(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	username := ctx.String("username")
	password := ctx.String("password")
	email := ctx.String("email")
	fullName := ctx.String("full-name")
	isAdmin := ctx.Bool("admin")
	restricted := ctx.Bool("restricted")
	prohibitLogin := ctx.Bool("prohibit-login")
	noMustChangePassword := ctx.Bool("no-must-change-password")
	visibility := ctx.String("visibility")

	// Get password from various sources in priority order
	if password == "" {
		if ctx.String("password-file") != "" {
			// Read from file
			content, err := os.ReadFile(ctx.String("password-file"))
			if err != nil {
				return fmt.Errorf("failed to read password file: %w", err)
			}
			password = strings.TrimSpace(string(content))
		} else if ctx.Bool("password-stdin") {
			// Read from stdin
			content, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read password from stdin: %w", err)
			}
			password = strings.TrimSpace(string(content))
		} else {
			// Interactive prompt (hidden input)
			fmt.Printf("Enter password for '%s': ", username)
			bytePassword, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			fmt.Println() // Add newline after hidden input
			password = string(bytePassword)

			if password == "" {
				return fmt.Errorf("password cannot be empty")
			}

			// Confirm password (only for interactive mode)
			fmt.Printf("Confirm password for '%s': ", username)
			bytePasswordConfirm, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				return fmt.Errorf("failed to read password confirmation: %w", err)
			}
			fmt.Println() // Add newline after hidden input
			passwordConfirm := string(bytePasswordConfirm)

			if password != passwordConfirm {
				return fmt.Errorf("passwords do not match")
			}
		}
	}

	if password == "" {
		return fmt.Errorf("password cannot be empty")
	}

	if email == "" {
		return fmt.Errorf("email is required")
	}

	client := ctx.Login.Client()

	// Build create options
	createOpts := gitea.CreateUserOption{
		LoginName:  username,
		Username:   username,
		Password:   password,
		Email:      email,
		FullName:   fullName,
		SendNotify: false,
	}

	// Set must change password flag (pointer to bool required)
	// By default, require user to change password on first login
	// Only set to false if --no-must-change-password flag is explicitly set
	mustChangePassword := !noMustChangePassword
	createOpts.MustChangePassword = &mustChangePassword

	vis, err := parseUserVisibility(visibility)
	if err != nil {
		return err
	}
	createOpts.Visibility = vis

	// Create the user
	user, _, err := client.AdminCreateUser(createOpts)
	if err != nil {
		return err
	}

	// Admin, Restricted, and ProhibitLogin cannot be set during user creation
	// We need to update them via AdminEditUser after creation if any of these flags are set
	if isAdmin || restricted || prohibitLogin {
		editOpts := gitea.EditUserOption{
			LoginName: username, // Required field
		}

		if isAdmin {
			editOpts.Admin = &isAdmin
		}

		if restricted {
			editOpts.Restricted = &restricted
		}

		if prohibitLogin {
			editOpts.ProhibitLogin = &prohibitLogin
		}

		// Update user with admin/restricted/prohibit-login settings
		_, err = client.AdminEditUser(username, editOpts)
		if err != nil {
			return fmt.Errorf("user created but failed to update admin/restricted/prohibit-login status: %w", err)
		}

		// Refresh user info to reflect the changes
		user, _, err = client.GetUserInfo(username)
		if err != nil {
			return fmt.Errorf("user updated but failed to retrieve updated user info: %w", err)
		}
	}

	print.UserDetails(user)

	return nil
}
