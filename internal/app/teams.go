// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var teamUnitNames = map[string]bool{"repo.code": true, "repo.issues": true, "repo.pulls": true, "repo.ext_issues": true, "repo.wiki": true, "repo.ext_wiki": true, "repo.releases": true, "repo.projects": true, "repo.packages": true, "repo.actions": true}

func (a *application) teamCommand() *cobra.Command {
	group := &cobra.Command{Use: "team", Short: "Manage organization teams and access"}
	var limit, page int
	list := &cobra.Command{Use: "list <org>", Short: "List organization teams", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		items, err := (services.Teams{Client: target.client}).List(cmd.Context(), args[0], limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, teamRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var name, description, permission string
		var createRepo, allRepos bool
		var units map[string]string
		command := &cobra.Command{Use: verb + " <id>", Short: titleCase(verb) + " an organization team", Args: argsExact(1)}
		switch verb {
		case "create":
			command.Use = "create <org>"
		case "delete":
			command.Annotations = textOutput("team deletion does not return a resource")
		}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&name, "name", "", "Team name")
			command.Flags().StringVar(&description, "description", "", "Team description")
			command.Flags().StringVar(&permission, "permission", "read", "Default access: read, write, admin")
			command.Flags().StringToStringVar(&units, "unit", nil, "Unit permissions, for example repo.code=write,repo.issues=read (replaces unit policy)")
			command.Flags().BoolVar(&createRepo, "can-create-repo", false, "Allow creating organization repositories")
			command.Flags().BoolVar(&allRepos, "all-repositories", false, "Access every organization repository")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			var id int64
			var err error
			prompted := false
			opts := services.TeamOptions{}
			if verb != "create" {
				id, err = number(args[0])
				if err != nil {
					return err
				}
			}
			if verb == "create" || verb == "edit" {
				if err := oneOf("permission", permission, "read", "write", "admin"); err != nil {
					return err
				}
				if cmd.Flags().Changed("unit") && (len(units) == 0 || permission == "admin") {
					return clierr.Wrap(clierr.Usage, errors.New("--unit needs a nonempty policy and cannot restrict administrator access"))
				}
				for unit, access := range units {
					if !teamUnitNames[unit] || (access != "none" && access != "read" && access != "write") {
						return clierr.Wrap(clierr.Usage, errors.New("--unit requires repo.* keys with none, read, or write access"))
					}
				}
				if verb == "create" {
					prompted, err = a.require(cmd, &name, "name")
					if err != nil {
						return err
					}
					opts.Name = &name
					opts.Permission = &permission
					opts.CanCreateRepo = &createRepo
					opts.AllRepositories = &allRepos
					if !cmd.Flags().Changed("unit") {
						opts.Units = map[string]string{"repo.code": permission, "repo.issues": permission, "repo.pulls": permission}
						if permission == "admin" {
							opts.Units = nil
						}
					}
				}
				if cmd.Flags().Changed("name") {
					if strings.TrimSpace(name) == "" {
						return clierr.Wrap(clierr.Usage, errors.New("team name cannot be empty"))
					}
					opts.Name = &name
				}
				if cmd.Flags().Changed("description") {
					opts.Description = &description
				}
				if cmd.Flags().Changed("permission") {
					opts.Permission = &permission
				}
				if cmd.Flags().Changed("unit") {
					opts.Units = units
				}
				if cmd.Flags().Changed("can-create-repo") {
					opts.CanCreateRepo = &createRepo
				}
				if cmd.Flags().Changed("all-repositories") {
					opts.AllRepositories = &allRepos
				}
				if verb == "edit" && opts.Name == nil && opts.Description == nil && opts.Permission == nil && opts.Units == nil && opts.CanCreateRepo == nil && opts.AllRepositories == nil {
					return clierr.Wrap(clierr.Usage, errors.New("supply team fields to edit"))
				}
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			service := services.Teams{Client: target.client}
			if verb == "delete" || prompted {
				subject := args[0]
				if verb == "create" {
					subject = name + " in " + args[0]
				}
				if err := a.confirm(cmd, verb+" team "+subject+" on "+target.Host); err != nil {
					return err
				}
			}
			var item services.Team
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), id)
			case "create":
				item, err = service.Create(cmd.Context(), args[0], opts)
			case "edit":
				item, err = service.Edit(cmd.Context(), id, opts)
			case "delete":
				return service.Delete(cmd.Context(), id)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, teamRow)
		}
		group.AddCommand(command)
	}
	group.AddCommand(a.teamAccessCommand(false), a.teamAccessCommand(true))
	return group
}

func (a *application) teamAccessCommand(repository bool) *cobra.Command {
	kind := "member"
	if repository {
		kind = "repo"
	}
	group := &cobra.Command{Use: kind, Short: "Manage team " + kind + " access"}
	var limit, page int
	list := &cobra.Command{Use: "list <id>", Short: "List team " + kind + "s", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		service := services.Teams{Client: target.client}
		if repository {
			items, err := service.Repositories(cmd.Context(), id, limit, page)
			if err != nil {
				return err
			}
			return writeList(a, cmd, items, func(item services.Repository) []terminal.Cell { return terminal.Row(item.FullName) })
		}
		items, err := service.Members(cmd.Context(), id, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, func(item services.OrganizationMember) []terminal.Cell { return terminal.Row(item.Username) })
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"add", "remove"} {
		use := verb + " <id> <username>"
		count := 2
		if repository {
			use = verb + " <id> --repo owner/repo"
			count = 1
		}
		group.AddCommand(&cobra.Command{Use: use, Short: titleCase(verb) + " team " + kind + " access", Annotations: textOutput("team access changes do not return a resource"), Args: argsExact(count), RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), repository)
			if err != nil {
				return err
			}
			if verb == "remove" {
				if err := a.confirm(cmd, "Remove team "+args[0]+" "+kind+" access"); err != nil {
					return err
				}
			}
			service := services.Teams{Client: target.client}
			if repository {
				return service.SetRepository(cmd.Context(), id, target.Repo, verb == "add")
			}
			return service.SetMember(cmd.Context(), id, args[1], verb == "add")
		}})
	}
	return group
}

func teamRow(item services.Team) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Name, item.Organization, item.Permission)
}
