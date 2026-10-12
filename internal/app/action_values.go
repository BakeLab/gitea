// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var actionValueName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (a *application) actionValueCommand(secret bool) *cobra.Command {
	kind := "variable"
	if secret {
		kind = "secret"
	}
	group := &cobra.Command{Use: kind, Short: "Manage scoped Actions " + kind + "s"}
	var org string
	var account bool
	group.PersistentFlags().StringVar(&org, "org", "", "Organization scope")
	group.PersistentFlags().BoolVar(&account, "account", false, "Current account scope across repositories")
	scope := func(cmd *cobra.Command) (services.ActionScope, target, error) {
		if (org != "" && account) || ((org != "" || account) && cmd.Flags().Changed("repo")) {
			return services.ActionScope{}, target{}, clierr.Wrap(clierr.Usage, errors.New("--org, --account, and --repo select mutually exclusive scopes"))
		}
		target, err := a.resolve(cmd.Context(), org == "" && !account)
		return services.ActionScope{Repository: target.Repo, Organization: org, Account: account}, target, err
	}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List " + kind + "s in the selected scope", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		selected, target, err := scope(cmd)
		if err != nil {
			return err
		}
		if secret {
			items, err := (services.ActionSecrets{Client: target.client, Scope: selected}).List(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			return writeList(a, cmd, items, secretRow)
		}
		items, err := (services.ActionVariables{Client: target.client, Scope: selected}).List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, variableRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	verbs := []string{"set", "delete"}
	if !secret {
		verbs = append(verbs, "view")
	}
	for _, verb := range verbs {
		var value, file, description string
		command := &cobra.Command{Use: verb + " <name>", Short: titleCase(verb) + " an Actions " + kind, Args: argsExact(1)}
		if verb == "set" {
			command.Flags().StringVar(&value, "value", "", "Value to store (use a file for secrets)")
			command.Flags().StringVar(&file, "value-file", "", "Read value from file, or - for stdin")
			command.Flags().StringVar(&description, "description", "", "Description")
		}
		if verb == "delete" {
			command.Annotations = textOutput(kind + " deletion does not return a resource")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !actionValueName.MatchString(name) {
				return clierr.Wrap(clierr.Usage, errors.New("name must contain letters, numbers, and underscores, and cannot start with a number"))
			}
			if strings.EqualFold(name, "CI") || strings.HasPrefix(strings.ToUpper(name), "GITEA_") || strings.HasPrefix(strings.ToUpper(name), "GITHUB_") {
				return clierr.Wrap(clierr.Usage, errors.New("CI and names beginning GITEA_ or GITHUB_ are reserved"))
			}
			prompted := false
			if verb == "set" {
				var err error
				if value, err = a.readSecret(cmd, value, file, "value", "value-file"); err != nil {
					return err
				}
				if !cmd.Flags().Changed("value") && !cmd.Flags().Changed("value-file") {
					snapshot, err := a.deps.Settings.Load()
					if err != nil {
						return err
					}
					if !a.policy(snapshot).CanPrompt(a.deps.Streams) {
						return clierr.Wrap(clierr.Usage, errors.New("missing value: supply --value or --value-file"))
					}
					if secret {
						value, err = a.deps.Prompter.SecretInput(cmd.Context(), "Secret value")
					} else {
						value, err = a.deps.Prompter.Input(cmd.Context(), "Variable value")
					}
					if err != nil {
						return err
					}
					prompted = true
				}
				if value == "" {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("%s value cannot be empty", kind))
				}
				if !secret && len(value) > 65536 {
					return clierr.Wrap(clierr.Usage, errors.New("value cannot exceed 64 KiB"))
				}
			}
			selected, target, err := scope(cmd)
			if err != nil {
				return err
			}
			if verb == "delete" || prompted {
				if err := a.confirm(cmd, verb+" Actions "+kind+" "+name); err != nil {
					return err
				}
			}
			if secret {
				service := services.ActionSecrets{Client: target.client, Scope: selected}
				if verb == "delete" {
					return service.Delete(cmd.Context(), name)
				}
				item, err := service.Set(cmd.Context(), name, value, description)
				if err != nil {
					return err
				}
				return writeItem(a, cmd, item, secretRow)
			}
			service := services.ActionVariables{Client: target.client, Scope: selected}
			if verb == "delete" {
				return service.Delete(cmd.Context(), name)
			}
			var item services.ActionVariable
			if verb == "view" {
				item, err = service.View(cmd.Context(), name)
			} else {
				item, err = service.Set(cmd.Context(), name, value, description)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, variableRow)
		}
		group.AddCommand(command)
	}
	return group
}

func secretRow(item services.ActionSecret) []terminal.Cell { return terminal.Row(item.Name) }

func variableRow(item services.ActionVariable) []terminal.Cell {
	return terminal.Row(item.Name, item.Value)
}
