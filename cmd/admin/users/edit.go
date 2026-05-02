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

// CmdUserEdit represents a sub command of users to edit a user
var CmdUserEdit = cli.Command{
	Name:        "edit",
	Aliases:     []string{"update", "e", "u"},
	Usage:       "Edit a user",
	Description: "Edit user account properties",
	ArgsUsage:   "<username>",
	Action:      RunUserEdit,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "password",
			Usage: "New password (use empty value --password=\"\" to trigger interactive prompt)",
			Value: "",
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
			Name:    "email",
			Aliases: []string{"e"},
			Usage:   "Email address",
		},
		&cli.StringFlag{
			Name:  "full-name",
			Usage: "Full name",
		},
		&cli.StringFlag{
			Name:  "description",
			Usage: "User description",
		},
		&cli.StringFlag{
			Name:  "website",
			Usage: "Website URL",
		},
		&cli.StringFlag{
			Name:  "location",
			Usage: "Location",
		},
		&cli.BoolFlag{
			Name:  "admin",
			Usage: "Make the user an administrator",
		},
		&cli.BoolFlag{
			Name:  "no-admin",
			Usage: "Remove administrator status",
		},
		&cli.BoolFlag{
			Name:  "restricted",
			Usage: "Make the user restricted",
		},
		&cli.BoolFlag{
			Name:  "no-restricted",
			Usage: "Remove restricted status",
		},
		&cli.BoolFlag{
			Name:  "prohibit-login",
			Usage: "Prohibit the user from logging in",
		},
		&cli.BoolFlag{
			Name:  "allow-login",
			Usage: "Allow the user to log in",
		},
		&cli.BoolFlag{
			Name:  "active",
			Usage: "Activate the user",
		},
		&cli.BoolFlag{
			Name:  "inactive",
			Usage: "Deactivate the user",
		},
		&cli.BoolFlag{
			Name:  "no-must-change-password",
			Usage: "Don't require the user to change password on next login (default: password change required)",
		},
		&cli.StringFlag{
			Name:  "visibility",
			Usage: "Visibility of the user profile (public, limited, private)",
		},
		&cli.IntFlag{
			Name:  "max-repo-creation",
			Usage: "Maximum number of repositories the user can create (-1 for unlimited)",
		},
		&cli.BoolFlag{
			Name:  "allow-git-hook",
			Usage: "Allow the user to use git hooks",
		},
		&cli.BoolFlag{
			Name:  "no-allow-git-hook",
			Usage: "Disallow the user from using git hooks",
		},
		&cli.BoolFlag{
			Name:  "allow-import-local",
			Usage: "Allow the user to import local repositories",
		},
		&cli.BoolFlag{
			Name:  "no-allow-import-local",
			Usage: "Disallow the user from importing local repositories",
		},
		&cli.BoolFlag{
			Name:  "allow-create-organization",
			Usage: "Allow the user to create organizations",
		},
		&cli.BoolFlag{
			Name:  "no-allow-create-organization",
			Usage: "Disallow the user from creating organizations",
		},
	}, flags.AllDefaultFlags...),
}

// RunUserEdit edits an existing user
func RunUserEdit(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	if ctx.Args().Len() == 0 {
		return fmt.Errorf("username is required")
	}

	client := ctx.Login.Client()
	username := ctx.Args().First()

	// Verify the user exists before attempting an update.
	_, _, err = client.GetUserInfo(username)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	// Build edit options, starting with required LoginName
	editOpts := gitea.EditUserOption{
		LoginName: username,
	}

	// Update email if set
	if ctx.IsSet("email") {
		email := ctx.String("email")
		editOpts.Email = &email
	}

	// Update full name if set
	if ctx.IsSet("full-name") {
		fullName := ctx.String("full-name")
		editOpts.FullName = &fullName
	}

	// Update description if set
	if ctx.IsSet("description") {
		description := ctx.String("description")
		editOpts.Description = &description
	}

	// Update website if set
	if ctx.IsSet("website") {
		website := ctx.String("website")
		editOpts.Website = &website
	}

	// Update location if set
	if ctx.IsSet("location") {
		location := ctx.String("location")
		editOpts.Location = &location
	}

	// Handle admin status
	if ctx.IsSet("admin") {
		admin := ctx.Bool("admin")
		editOpts.Admin = &admin
	} else if ctx.IsSet("no-admin") {
		admin := false
		editOpts.Admin = &admin
	}

	// Handle restricted status
	if ctx.IsSet("restricted") {
		restricted := ctx.Bool("restricted")
		editOpts.Restricted = &restricted
	} else if ctx.IsSet("no-restricted") {
		restricted := false
		editOpts.Restricted = &restricted
	}

	// Handle prohibit login status
	if ctx.IsSet("prohibit-login") {
		prohibitLogin := ctx.Bool("prohibit-login")
		editOpts.ProhibitLogin = &prohibitLogin
	} else if ctx.IsSet("allow-login") {
		prohibitLogin := false
		editOpts.ProhibitLogin = &prohibitLogin
	}

	// Handle active status
	if ctx.IsSet("active") {
		active := ctx.Bool("active")
		editOpts.Active = &active
	} else if ctx.IsSet("inactive") {
		active := false
		editOpts.Active = &active
	}

	// Handle must change password - will be set when password is changed unless flag is set

	// Handle visibility
	if ctx.IsSet("visibility") {
		vis, err := parseUserVisibility(ctx.String("visibility"))
		if err != nil {
			return err
		}
		editOpts.Visibility = vis
	}

	// Handle max repo creation
	if ctx.IsSet("max-repo-creation") {
		maxRepoCreation := ctx.Int("max-repo-creation")
		editOpts.MaxRepoCreation = &maxRepoCreation
	}

	// Handle allow git hook
	if ctx.IsSet("allow-git-hook") {
		allowGitHook := ctx.Bool("allow-git-hook")
		editOpts.AllowGitHook = &allowGitHook
	} else if ctx.IsSet("no-allow-git-hook") {
		allowGitHook := false
		editOpts.AllowGitHook = &allowGitHook
	}

	// Handle allow import local
	if ctx.IsSet("allow-import-local") {
		allowImportLocal := ctx.Bool("allow-import-local")
		editOpts.AllowImportLocal = &allowImportLocal
	} else if ctx.IsSet("no-allow-import-local") {
		allowImportLocal := false
		editOpts.AllowImportLocal = &allowImportLocal
	}

	// Handle allow create organization
	if ctx.IsSet("allow-create-organization") {
		allowCreateOrg := ctx.Bool("allow-create-organization")
		editOpts.AllowCreateOrganization = &allowCreateOrg
	} else if ctx.IsSet("no-allow-create-organization") {
		allowCreateOrg := false
		editOpts.AllowCreateOrganization = &allowCreateOrg
	}

	// Handle password if any password flag is set or if password flag was provided (even without value)
	shouldChangePassword := ctx.IsSet("password") || ctx.IsSet("password-file") || ctx.Bool("password-stdin")
	if shouldChangePassword {
		password := ctx.String("password")

		// Get password from various sources in priority order
		if password == "" {
			if ctx.IsSet("password-file") && ctx.String("password-file") != "" {
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
				// Interactive prompt (hidden input) - triggered when --password is used without value
				fmt.Printf("Enter new password for '%s': ", username)
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
				fmt.Printf("Confirm new password for '%s': ", username)
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

		editOpts.Password = password

		// When password is changed, require user to change password on next login by default
		// Only set to false if --no-must-change-password flag is explicitly set
		if !ctx.IsSet("no-must-change-password") {
			mustChangePassword := true
			editOpts.MustChangePassword = &mustChangePassword
		} else {
			mustChangePassword := false
			editOpts.MustChangePassword = &mustChangePassword
		}
	}

	// Only proceed with update if at least one field is being modified
	hasChanges := editOpts.Email != nil ||
		editOpts.FullName != nil ||
		editOpts.Description != nil ||
		editOpts.Website != nil ||
		editOpts.Location != nil ||
		editOpts.Admin != nil ||
		editOpts.Restricted != nil ||
		editOpts.ProhibitLogin != nil ||
		editOpts.Active != nil ||
		editOpts.Visibility != nil ||
		editOpts.MaxRepoCreation != nil ||
		editOpts.AllowGitHook != nil ||
		editOpts.AllowImportLocal != nil ||
		editOpts.AllowCreateOrganization != nil ||
		editOpts.Password != ""

	if !hasChanges {
		return fmt.Errorf("no changes specified")
	}

	// Update the user
	_, err = client.AdminEditUser(username, editOpts)
	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	// Refresh user info to reflect the changes
	updatedUser, _, err := client.GetUserInfo(username)
	if err != nil {
		return fmt.Errorf("user updated but failed to retrieve updated user info: %w", err)
	}

	print.UserDetails(updatedUser)
	return nil
}
