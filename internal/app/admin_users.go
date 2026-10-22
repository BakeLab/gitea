// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) adminCommand() *cobra.Command {
	group := &cobra.Command{Use: "admin", Short: "Manage instance administration", Long: "Administer the Gitea instance. These commands need an administrator account."}
	users := &cobra.Command{Use: "user", Short: "Manage registered accounts", Long: `List, view, create, edit and delete the accounts of the instance. These commands need an
administrator account.`}
	users.AddCommand(a.adminUserListCommand(), a.adminUserViewCommand(), a.adminUserChangeCommand("create"), a.adminUserChangeCommand("edit"), a.adminUserDeleteCommand())
	group.AddCommand(jsonOutput(users, jsonFields[services.AdminUser]()))
	return group
}

// completeAdminUsers completes the usernames of the instance with their names.
func (a *application) completeAdminUsers() cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, toComplete string) ([]cobra.Completion, error) {
		users, err := (services.AdminUsers{Client: t.client}).List(ctx, toComplete, completionLimit, 1)
		return describe(users.Items, func(user services.AdminUser) (string, string) { return user.Username, user.FullName }), err
	})
}

// userRole is admin, restricted or user.
func userRole(user services.AdminUser) terminal.Cell {
	switch {
	case user.Admin:
		return terminal.Cell{Text: "admin", Color: "magenta"}
	case user.Restricted:
		return terminal.Cell{Text: "restricted", Color: "yellow"}
	}
	return terminal.Cell{Text: "user"}
}

// userStatus is active, inactive or login prohibited.
func userStatus(user services.AdminUser) terminal.Cell {
	switch {
	case user.ProhibitLogin:
		return terminal.Cell{Text: "login prohibited", Color: "red"}
	case !user.Active:
		return terminal.Cell{Text: "inactive", Color: "yellow"}
	}
	return terminal.Cell{Text: "active", Color: "green"}
}

func (a *application) adminUserListCommand() *cobra.Command {
	var query string
	var limit, page int
	cmd := &cobra.Command{
		Use: "list", Short: "List registered accounts", Args: argsExact(0),
		Example: `$ gt admin user list
# find accounts by username, name or email
$ gt admin user list --query bob
# print the email addresses
$ gt admin user list --jq '.[].email'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			users, err := (services.AdminUsers{Client: target.client}).List(cmd.Context(), query, limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.AdminUser]{
				Columns: []terminal.Column{{Header: "ID"}, {Header: "Username"}, {Header: "Name", Flex: true}, {Header: "Email"}, {Header: "Role"}, {Header: "Status"}, {Header: "Created"}},
				Row: func(user services.AdminUser) []terminal.Cell {
					return []terminal.Cell{
						{Text: strconv.FormatInt(user.ID, 10), Link: user.URL},
						{Text: user.Username, Bold: true},
						{Text: user.FullName},
						{Text: user.Email},
						userRole(user), userStatus(user),
						{Time: terminal.ParseTime(user.CreatedAt)},
					}
				},
				Context: "users on " + target.Host, Empty: "no users on " + target.Host,
			}
			if query != "" {
				spec.Context, spec.Empty = fmt.Sprintf("users matching %q", query), fmt.Sprintf("no users match %q", query)
			}
			return writeTable(a, cmd, users.Items, spec, servicePage(users, page))
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Only list accounts whose username, name or email contains this text")
	pageFlags(cmd, &limit, &page)
	return cmd
}

func (a *application) adminUserViewCommand() *cobra.Command {
	return &cobra.Command{
		Use: "view <username>", Short: "View an account", Args: argsExact(1), ValidArgsFunction: a.completeAdminUsers(),
		Example: `$ gt admin user view bob
$ gt admin user view bob --json email,admin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			user, err := (services.AdminUsers{Client: target.client}).View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			d := a.display()
			return writeView(a, cmd, user, func(user services.AdminUser) terminal.View {
				created := timeField("Created", user.CreatedAt, d)
				meta := ""
				if created.Value != "" {
					meta = "joined " + created.Value
				}
				role, status := userRole(user), userStatus(user)
				created.PipedOnly = true
				return terminal.View{
					Title: user.Username, ID: fmt.Sprintf("(ID %d)", user.ID), Badges: []terminal.Cell{role, status}, Meta: meta,
					Fields: []terminal.Field{
						{Label: "ID", Value: strconv.FormatInt(user.ID, 10), PipedOnly: true},
						{Label: "Name", Value: user.FullName},
						{Label: "Email", Value: user.Email},
						{Label: "Role", Value: role.Text, PipedOnly: true},
						{Label: "Status", Value: status.Text, PipedOnly: true},
						{Label: "Visibility", Value: user.Visibility},
						{Label: "Website", Value: user.Website},
						{Label: "Location", Value: user.Location},
						created,
					},
					Body: user.Description, Kind: "user", URL: user.URL,
				}
			})
		},
	}
}

func (a *application) adminUserChangeCommand(verb string) *cobra.Command {
	var username, email, fullName, description, website, location, visibility, password, passwordFile string
	var admin, active, restricted, prohibitLogin, mustChangePassword, allowGitHook, allowImportLocal, allowCreateOrganization bool
	var maxRepos int
	opts := services.AdminUserOptions{}
	command := &cobra.Command{
		Use: "edit <username>", Short: "Edit an account", Args: argsExact(1), ValidArgsFunction: a.completeAdminUsers(),
		Example: `$ gt admin user edit bob --full-name "Bob Builder" --restricted
# reset a password and require a new one on the next sign-in
$ gt admin user edit bob --password-file pw.txt --must-change-password
# revoke administrator privileges
$ gt admin user edit bob --admin=false`,
	}
	if verb == "create" {
		command.Use, command.Short, command.ValidArgsFunction = "create [<username>]", "Create an account", nil
		command.Args = nonBlankArgs(cobra.MaximumNArgs(1), "username")
		command.Long = `Create an account. On a terminal, gt asks for a missing username, email address and
password; the new account must change its password on its first sign-in unless
--must-change-password=false is passed.`
		command.Example = `$ gt admin user create carol --email carol@example.com
# read the password from a file instead of a prompt
$ gt admin user create ci-bot --email ci@example.com --password-file pw.txt --must-change-password=false`
		command.Flags().StringVar(&username, "username", "", "Account username")
	}
	type stringBinding struct {
		name, description string
		value             *string
		option            **string
	}
	stringsToBind := []stringBinding{{"email", "Email address", &email, &opts.Email}, {"full-name", "Display name", &fullName, &opts.FullName}}
	type boolBinding struct {
		name, description string
		value             *bool
		option            **bool
	}
	boolsToBind := []boolBinding{{"admin", "Administrator privileges", &admin, &opts.Admin}, {"restricted", "Restrict account access", &restricted, &opts.Restricted}, {"prohibit-login", "Prohibit signing in", &prohibitLogin, &opts.ProhibitLogin}}
	if verb == "edit" {
		stringsToBind = append(stringsToBind, stringBinding{"description", "Profile description", &description, &opts.Description}, stringBinding{"website", "Website URL", &website, &opts.Website}, stringBinding{"location", "Location", &location, &opts.Location})
		boolsToBind = append(boolsToBind, boolBinding{"active", "Activate account", &active, &opts.Active}, boolBinding{"allow-git-hook", "Allow Git hooks", &allowGitHook, &opts.AllowGitHook}, boolBinding{"allow-import-local", "Allow local repository imports", &allowImportLocal, &opts.AllowImportLocal}, boolBinding{"allow-create-organization", "Allow creating organizations", &allowCreateOrganization, &opts.AllowCreateOrganization})
		command.Flags().IntVar(&maxRepos, "max-repo-creation", 0, "Repository creation limit (-1 uses the instance default)")
	}
	for _, binding := range stringsToBind {
		command.Flags().StringVar(binding.value, binding.name, "", binding.description)
	}
	visibilityUsage := "Profile visibility"
	if verb == "create" {
		visibilityUsage += ", by default the instance's default"
	}
	enumFlag(command, &visibility, "visibility", "", "", visibilityUsage, visibilities...)
	for _, binding := range boolsToBind {
		command.Flags().BoolVar(binding.value, binding.name, false, binding.description+" (use =false to revoke)")
	}
	command.Flags().StringVar(&password, "password", "", "Password (prefer --password-file)")
	command.Flags().StringVar(&passwordFile, "password-file", "", "Read password from file or - for stdin; remove one trailing line ending")
	command.Flags().BoolVar(&mustChangePassword, "must-change-password", verb == "create", "Require password change on next sign-in (use =false to disable)")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if verb == "create" {
			if len(args) > 0 {
				if cmd.Flags().Changed("username") {
					return clierr.Wrap(clierr.Usage, errors.New("a username argument cannot be combined with --username"))
				}
				username = args[0]
			}
			if _, err := a.requireInput(cmd, &username, "username", "Username", validUsername); err != nil {
				return err
			}
			if _, err := a.requireInput(cmd, &email, "email", "Email address", validEmail); err != nil {
				return err
			}
			opts.Email, opts.FullName = &email, &fullName
		} else {
			username = args[0]
		}
		if err := validUsername(username); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		for _, binding := range stringsToBind {
			if cmd.Flags().Changed(binding.name) {
				*binding.option = binding.value
			}
		}
		if cmd.Flags().Changed("visibility") {
			opts.Visibility = &visibility
		}
		for _, binding := range boolsToBind {
			if cmd.Flags().Changed(binding.name) {
				*binding.option = binding.value
			}
		}
		if opts.Email != nil {
			if err := validEmail(email); err != nil {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--email: %w", err))
			}
		}
		if cmd.Flags().Changed("max-repo-creation") {
			if maxRepos < -1 {
				return clierr.Wrap(clierr.Usage, errors.New("--max-repo-creation must be -1 or nonnegative"))
			}
			opts.MaxRepoCreation = &maxRepos
		}
		if verb == "create" || cmd.Flags().Changed("password") || cmd.Flags().Changed("password-file") {
			var err error
			if opts.Password, err = a.adminUserPassword(cmd, username, password, passwordFile); err != nil {
				return err
			}
		}
		if verb == "create" || cmd.Flags().Changed("must-change-password") {
			opts.MustChangePassword = &mustChangePassword
		}
		if verb == "edit" && opts == (services.AdminUserOptions{}) {
			return clierr.Wrap(clierr.Usage, errors.New("supply account fields to edit"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		service := services.AdminUsers{Client: target.client}
		if verb == "edit" {
			user, err := service.Edit(cmd.Context(), username, opts)
			if err != nil {
				return err
			}
			a.done(cmd, "Edited user %s", user.Username)
			return a.jsonResult(cmd, user)
		}
		user, err := service.Create(cmd.Context(), username, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Created user %s", user.Username)
		return a.created(cmd, user, user.URL)
	}
	return command
}

// adminUserPassword reads the password from its flags, or asks twice on a terminal.
func (a *application) adminUserPassword(cmd *cobra.Command, username, password, passwordFile string) (string, error) {
	password, err := a.readSecret(cmd, password, passwordFile, "password", "password-file")
	if err != nil {
		return "", err
	}
	if password == "" && !cmd.Flags().Changed("password-file") {
		if !a.canPrompt() {
			return "", clierr.Wrap(clierr.Usage, errors.New("missing password: supply --password-file or --password"))
		}
		if password, err = a.deps.Prompter.SecretInput(cmd.Context(), "Password for "+username); err != nil {
			return "", err
		}
		confirmation, err := a.deps.Prompter.SecretInput(cmd.Context(), "Confirm password")
		if err != nil {
			return "", err
		}
		if password != confirmation {
			return "", clierr.Wrap(clierr.Usage, errors.New("passwords do not match"))
		}
	}
	if password == "" {
		return "", clierr.Wrap(clierr.Usage, errors.New("password cannot be empty"))
	}
	return password, nil
}

func validUsername(username string) error {
	if strings.TrimSpace(username) == "" || strings.ContainsAny(username, "/\\ \t\r\n") {
		return errors.New("invalid account username")
	}
	return nil
}

func validEmail(email string) error {
	if address, err := mail.ParseAddress(email); err != nil || address.Address != email {
		return errors.New("enter one email address, such as carol@example.com")
	}
	return nil
}

func (a *application) adminUserDeleteCommand() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use: "delete <username>", Short: "Delete an account", Args: argsExact(1), ValidArgsFunction: a.completeAdminUsers(),
		Long: `Delete an account. Gitea refuses while the user owns repositories or organizations, unless
--purge deletes the account with everything it owns.`,
		Example: `$ gt admin user delete spammer
# also delete the user's repositories, packages and organization memberships (and organizations they solely own)
$ gt admin user delete spammer --purge`,
		Annotations: map[string]string{"gt.text": "account deletion does not return a resource", "gt.confirm": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validUsername(args[0]); err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.AdminUsers{Client: target.client}
			user, err := service.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			confirmation := terminal.Confirmation{Prompt: "Delete user " + user.Username + "?"}
			if purge {
				confirmation = terminal.Confirmation{
					Prompt: fmt.Sprintf("Delete user %s with their repositories, packages and organization memberships, and the organizations they are the last owner of with those organizations' repositories? This cannot be undone.", user.Username),
					Typed:  user.Username, NonTTY: "purge user " + user.Username,
				}
			}
			if err := a.confirmAction(cmd, confirmation); err != nil {
				return err
			}
			err = service.Delete(cmd.Context(), user.Username, purge)
			if conflict, ok := errors.AsType[*services.ConflictError](err); ok && (conflict.Reason == services.ReasonOwnsRepos || conflict.Reason == services.ReasonOwnsOther) {
				conflict.Hint = "transfer or delete them, or pass --purge to delete them with the user"
				if conflict.Reason == services.ReasonOwnsRepos {
					conflict.Hint = "transfer them with gt repo transfer or delete them, or pass --purge to delete them with the user"
				}
			}
			if err != nil {
				return err
			}
			if purge {
				a.done(cmd, "Deleted user %s and everything they owned", user.Username)
			} else {
				a.done(cmd, "Deleted user %s", user.Username)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete the user's repositories, packages, organization memberships and the organizations they solely own")
	return cmd
}
