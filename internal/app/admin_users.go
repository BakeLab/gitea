// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"net/mail"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) adminCommand() *cobra.Command {
	group := &cobra.Command{Use: "admin", Short: "Manage instance administration"}
	users := &cobra.Command{Use: "user", Short: "Manage registered accounts"}
	group.AddCommand(users)
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List registered accounts", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, err := (services.AdminUsers{Client: target.client}).List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, adminUserRow)
	}}
	pageFlags(list, &limit, &page)
	users.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var username, email, fullName, description, website, location, visibility, password, passwordFile string
		var admin, active, restricted, prohibitLogin, mustChangePassword, allowGitHook, allowImportLocal, allowCreateOrganization bool
		var maxRepos int
		opts := services.AdminUserOptions{}
		command := &cobra.Command{Use: verb + " <username>", Short: titleCase(verb) + " an account", Args: argsExact(1)}
		if verb == "create" {
			command.Use = "create [username]"
			command.Args = usageArgs(cobra.MaximumNArgs(1))
			command.Flags().StringVar(&username, "username", "", "Account username")
		}
		if verb == "delete" {
			command.Annotations = textOutput("account deletion does not return a resource")
		}
		type stringBinding struct {
			name, description string
			value             *string
			option            **string
		}
		stringsToBind := []stringBinding{{"email", "Email address", &email, &opts.Email}, {"full-name", "Display name", &fullName, &opts.FullName}, {"visibility", "Profile visibility: public, limited, private", &visibility, &opts.Visibility}}
		type boolBinding struct {
			name, description string
			value             *bool
			option            **bool
		}
		boolsToBind := []boolBinding{{"admin", "Administrator privileges", &admin, &opts.Admin}, {"restricted", "Restrict account access", &restricted, &opts.Restricted}, {"prohibit-login", "Prohibit signing in", &prohibitLogin, &opts.ProhibitLogin}}
		if verb == "edit" {
			stringsToBind = append(stringsToBind, stringBinding{"description", "Profile description", &description, &opts.Description}, stringBinding{"website", "Website URL", &website, &opts.Website}, stringBinding{"location", "Location", &location, &opts.Location})
			boolsToBind = append(boolsToBind, boolBinding{"active", "Activate account", &active, &opts.Active}, boolBinding{"allow-git-hook", "Allow Git hooks", &allowGitHook, &opts.AllowGitHook}, boolBinding{"allow-import-local", "Allow local repository imports", &allowImportLocal, &opts.AllowImportLocal}, boolBinding{"allow-create-organization", "Allow creating organizations", &allowCreateOrganization, &opts.AllowCreateOrganization})
			command.Flags().IntVar(&maxRepos, "max-repo-creation", 0, "Repository creation limit (-1 for unlimited)")
		}
		if verb == "create" || verb == "edit" {
			for _, binding := range stringsToBind {
				command.Flags().StringVar(binding.value, binding.name, "", binding.description)
			}
			for _, binding := range boolsToBind {
				command.Flags().BoolVar(binding.value, binding.name, false, binding.description+" (use =false to revoke)")
			}
			command.Flags().StringVar(&password, "password", "", "Password (prefer --password-file)")
			command.Flags().StringVar(&passwordFile, "password-file", "", "Read password from file or - for stdin; remove one trailing line ending")
			command.Flags().BoolVar(&mustChangePassword, "must-change-password", true, "Require password change on next sign-in (use =false to disable)")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			prompted := false
			if verb == "create" {
				if len(args) > 0 {
					if cmd.Flags().Changed("username") {
						return clierr.Wrap(clierr.Usage, errors.New("supply username as an argument or --username, not both"))
					}
					username = args[0]
				}
				for _, field := range []struct {
					value *string
					flag  string
				}{{&username, "username"}, {&email, "email"}} {
					completed, err := a.require(cmd, field.value, field.flag)
					if err != nil {
						return err
					}
					prompted = prompted || completed
				}
				opts.Email = &email
				opts.FullName = &fullName
				if visibility == "" {
					visibility = "public"
				}
				opts.Visibility = &visibility
			} else {
				username = args[0]
			}
			if strings.TrimSpace(username) == "" || strings.ContainsAny(username, "/\\ \t\r\n") {
				return clierr.Wrap(clierr.Usage, errors.New("invalid account username"))
			}
			if verb == "create" || verb == "edit" {
				for _, binding := range stringsToBind {
					if cmd.Flags().Changed(binding.name) {
						*binding.option = binding.value
					}
				}
				for _, binding := range boolsToBind {
					if cmd.Flags().Changed(binding.name) {
						*binding.option = binding.value
					}
				}
				if opts.Email != nil {
					address, err := mail.ParseAddress(email)
					if err != nil || address.Address != email {
						return clierr.Wrap(clierr.Usage, errors.New("--email must contain one email address"))
					}
				}
				if opts.Visibility != nil {
					if err := oneOf("visibility", visibility, "public", "limited", "private"); err != nil {
						return err
					}
				}
				if cmd.Flags().Changed("max-repo-creation") {
					if maxRepos < -1 {
						return clierr.Wrap(clierr.Usage, errors.New("--max-repo-creation must be -1 or nonnegative"))
					}
					opts.MaxRepoCreation = &maxRepos
				}
				passwordChange := verb == "create" || cmd.Flags().Changed("password") || cmd.Flags().Changed("password-file")
				if passwordChange {
					var err error
					if password, err = a.readSecret(cmd, password, passwordFile, "password", "password-file"); err != nil {
						return err
					}
					if password == "" && !cmd.Flags().Changed("password-file") {
						snapshot, err := a.deps.Settings.Load()
						if err != nil {
							return err
						}
						if !a.policy(snapshot).CanPrompt(a.deps.Streams) {
							return clierr.Wrap(clierr.Usage, errors.New("missing password: supply --password-file or --password"))
						}
						password, err = a.deps.Prompter.SecretInput(cmd.Context(), "Password for "+username)
						if err != nil {
							return err
						}
						confirmation, err := a.deps.Prompter.SecretInput(cmd.Context(), "Confirm password")
						if err != nil {
							return err
						}
						if password != confirmation {
							return clierr.Wrap(clierr.Usage, errors.New("passwords do not match"))
						}
						prompted = true
					}
					if password == "" {
						return clierr.Wrap(clierr.Usage, errors.New("password cannot be empty"))
					}
					opts.Password = password
					opts.MustChangePassword = &mustChangePassword
				} else if cmd.Flags().Changed("must-change-password") {
					opts.MustChangePassword = &mustChangePassword
				}

				if verb == "edit" && opts == (services.AdminUserOptions{}) {
					return clierr.Wrap(clierr.Usage, errors.New("supply account fields to edit"))
				}
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			if verb == "delete" || prompted {
				if err := a.confirm(cmd, verb+" account "+username+" on "+target.Host); err != nil {
					return err
				}
			}
			service := services.AdminUsers{Client: target.client}
			var item services.AdminUser
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), username)
			case "create":
				item, err = service.Create(cmd.Context(), username, opts)
			case "edit":
				item, err = service.Edit(cmd.Context(), username, opts)
			case "delete":
				return service.Delete(cmd.Context(), username)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, adminUserRow)
		}
		users.AddCommand(command)
	}
	return group
}

func adminUserRow(item services.AdminUser) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Username, item.FullName, "admin="+strconv.FormatBool(item.Admin), "active="+strconv.FormatBool(item.Active))
}
