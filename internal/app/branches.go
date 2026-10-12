// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// validateBranch rejects invalid Git refs before making requests.
func validateBranch(name string) error {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") || strings.ContainsAny(name, " ~^:?*[\\\t\r\n") {
		return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
	}
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
		}
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return clierr.Wrap(clierr.Usage, errors.New("invalid branch name"))
		}
	}
	return nil
}

func (a *application) branchCommand() *cobra.Command {
	group := &cobra.Command{Use: "branch", Short: "Manage remote branches and protection rules"}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List remote branches", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Branches{Client: target.client}).List(cmd.Context(), target.Repo, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, branchRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "rename", "delete", "protect", "unprotect"} {
		var source string
		count := 1
		use := verb + " <name>"
		if verb == "rename" {
			count = 2
			use = "rename <old-name> <new-name>"
		}
		arguments := argsExact(count)
		if verb == "protect" || verb == "unprotect" {
			use = verb + " <name>..."
			arguments = usageArgs(cobra.MinimumNArgs(1))
		}
		command := &cobra.Command{Use: use, Short: titleCase(verb) + " a remote branch", Args: arguments, RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range args {
				if err := validateBranch(name); err != nil {
					return err
				}
			}
			if source != "" {
				if err := validateBranch(source); err != nil {
					return err
				}
			}
			if verb == "rename" && args[0] == args[1] {
				return clierr.Wrap(clierr.Usage, errors.New("new branch name must differ from the current name"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Branches{Client: target.client}
			if verb == "delete" || verb == "unprotect" {
				if err := a.confirm(cmd, verb+" branches "+strings.Join(args, ", ")+" in "+target.Repo); err != nil {
					return err
				}
			}
			switch verb {
			case "view":
				item, err := service.View(cmd.Context(), target.Repo, args[0])
				if err != nil {
					return err
				}
				return writeItem(a, cmd, item, branchRow)
			case "create":
				item, err := service.Create(cmd.Context(), target.Repo, args[0], source)
				if err != nil {
					return err
				}
				return writeItem(a, cmd, item, branchRow)
			case "rename":
				return service.Rename(cmd.Context(), target.Repo, args[0], args[1])
			case "delete":
				return service.Delete(cmd.Context(), target.Repo, args[0])
			case "protect", "unprotect":
				var failures []error
				for _, name := range args {
					var err error
					if verb == "protect" {
						err = service.Protect(cmd.Context(), target.Repo, name)
					} else {
						err = service.Unprotect(cmd.Context(), target.Repo, name)
					}
					if err != nil {
						failures = append(failures, fmt.Errorf("%s branch %s: %w", verb, name, err))
						continue
					}
					if len(args) > 1 {
						if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s branch %s\n", verb, name); err != nil {
							return err
						}
					}
				}
				return errors.Join(failures...)
			}
			return nil
		}}
		if verb == "create" {
			command.Flags().StringVar(&source, "source", "", "Source branch (default: repository default branch)")
		}
		if verb != "view" && verb != "create" {
			command.Annotations = textOutput("branch " + verb + " does not return a resource")
		}
		group.AddCommand(command)
	}
	return group
}

func branchRow(item services.Branch) []terminal.Cell {
	return terminal.Row(item.Name, item.SHA, strconv.FormatBool(item.Protected))
}
