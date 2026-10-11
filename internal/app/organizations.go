// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func organizationRow(item services.Organization) []terminal.Cell {
	return terminal.Row(item.Name, item.FullName, item.Visibility)
}

func (a *application) organizationCommand() *cobra.Command {
	group := &cobra.Command{Use: "org", Short: "Manage organizations"}
	var all bool
	var username string
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List organizations on the selected instance", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if all && username != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--all and --username are mutually exclusive"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, more, err := (services.Organizations{Client: target.client}).List(cmd.Context(), !all && username == "", username, limit, page)
		if err != nil {
			return err
		}
		if err := writeList(a, cmd, items, organizationRow); err != nil {
			return err
		}
		return a.morePagesHint(cmd, more, page)
	}}
	list.Flags().BoolVar(&all, "all", false, "List public organizations instead of your memberships")
	list.Flags().StringVar(&username, "username", "", "List organizations for a username")
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var fullName, description, website, location, visibility string
		var adminAccess bool
		command := &cobra.Command{Use: verb + " <name>", Short: titleCase(verb) + " an organization", Args: argsExact(1)}
		if verb == "delete" {
			command.Annotations = textOutput("organization deletion does not return a resource")
		}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&fullName, "full-name", "", "Display name")
			command.Flags().StringVar(&description, "description", "", "Organization description")
			command.Flags().StringVar(&website, "website", "", "Website URL")
			command.Flags().StringVar(&location, "location", "", "Location")
			command.Flags().StringVar(&visibility, "visibility", "public", "public, limited, or private")
			command.Flags().BoolVar(&adminAccess, "repo-admins-can-change-team-access", false, "Allow repository administrators to change team access")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if strings.TrimSpace(name) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("organization name cannot be empty"))
			}
			opts := services.OrganizationOptions{}
			if verb == "create" {
				opts.Name = &name
				opts.Visibility = &visibility
				opts.RepoAdminChangeTeamAccess = &adminAccess
			}
			if verb == "create" || verb == "edit" {
				if err := oneOf("visibility", visibility, "public", "limited", "private"); err != nil {
					return err
				}
				if cmd.Flags().Changed("full-name") {
					opts.FullName = &fullName
				}
				if cmd.Flags().Changed("description") {
					opts.Description = &description
				}
				if cmd.Flags().Changed("website") {
					opts.Website = &website
				}
				if cmd.Flags().Changed("location") {
					opts.Location = &location
				}
				if cmd.Flags().Changed("visibility") {
					opts.Visibility = &visibility
				}
				if cmd.Flags().Changed("repo-admins-can-change-team-access") {
					opts.RepoAdminChangeTeamAccess = &adminAccess
				}
				if verb == "edit" && opts == (services.OrganizationOptions{}) {
					return clierr.Wrap(clierr.Usage, errors.New("supply organization fields to edit"))
				}
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			if verb == "delete" {
				if err := a.confirm(cmd, "Delete organization "+name); err != nil {
					return err
				}
				return service.Delete(cmd.Context(), name)
			}
			var item services.Organization
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), name)
			case "create":
				item, err = service.Create(cmd.Context(), opts)
			case "edit":
				item, err = service.Edit(cmd.Context(), name, opts)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, organizationRow)
		}
		group.AddCommand(command)
	}
	group.AddCommand(a.organizationMemberCommand())
	return group
}

func (a *application) organizationMemberCommand() *cobra.Command {
	group := &cobra.Command{Use: "member", Short: "Manage organization membership"}
	var public bool
	var limit, page int
	list := &cobra.Command{Use: "list <org>", Short: "List organization members", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, err := (services.Organizations{Client: target.client}).Members(cmd.Context(), args[0], public, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, func(item services.OrganizationMember) []terminal.Cell {
			return terminal.Row(item.Username, item.FullName)
		})
	}}
	list.Flags().BoolVar(&public, "public", false, "List publicly visible memberships")
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"remove", "public", "private"} {
		group.AddCommand(&cobra.Command{Use: verb + " <org> <username>", Short: titleCase(verb) + " an organization membership", Annotations: textOutput("membership changes do not return a resource"), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Organizations{Client: target.client}
			if verb == "remove" {
				if err := a.confirm(cmd, "Remove "+args[1]+" from organization "+args[0]); err != nil {
					return err
				}
				return service.RemoveMember(cmd.Context(), args[0], args[1])
			}
			return service.SetMemberPublic(cmd.Context(), args[0], args[1], verb == "public")
		}})
	}
	return group
}
