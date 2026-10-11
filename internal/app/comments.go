// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func commentRecord(item services.Comment) terminal.Record {
	return terminal.Record{Heading: terminal.Row(strconv.FormatInt(item.ID, 10), item.Author), Lines: []string{item.Body, item.URL}}
}

func (a *application) commentCommand() *cobra.Command {
	group := &cobra.Command{Use: "comment", Short: "Manage discussion comments"}
	var limit, page int
	list := &cobra.Command{Use: "list <number>", Short: "List comments on an issue or pull request", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Comments{Client: target.client, Repository: target.Repo}).Page(cmd.Context(), n, limit, page)
		if err != nil {
			return err
		}
		return writeRecords(a, cmd, items, commentRecord)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var body, file string
		var useEditor bool
		noun := "id"
		if verb == "create" {
			noun = "number"
		}
		command := &cobra.Command{Use: verb + " <" + noun + ">", Short: titleCase(verb) + " a discussion comment", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var draft terminal.Draft
			defer finishDraft(&draft, &runErr)
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			id, err := number(args[0])
			if err != nil {
				return err
			}
			prompted := false
			if (verb == "create" || verb == "edit") && !useEditor {
				body, err = a.readBody(cmd, body, file)
				if err != nil {
					return err
				}
				if !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
					prompted, err = a.require(cmd, &body, "body")
					if err != nil {
						return err
					}
				}
				if strings.TrimSpace(body) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("comment body cannot be empty"))
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Comments{Client: target.client, Repository: target.Repo}
			if verb == "create" || verb == "edit" {
				if useEditor {
					initial := ""
					if verb == "edit" {
						current, viewErr := service.View(cmd.Context(), id)
						if viewErr != nil {
							return viewErr
						}
						initial = current.Body
					}
					draft, err = a.editDraft(cmd, initial)
					if err != nil {
						return err
					}
					body = draft.Body
				}
				if strings.TrimSpace(body) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("comment body cannot be empty"))
				}
			}
			if verb == "delete" || prompted {
				description := fmt.Sprintf("%s comment %d on %s", titleCase(verb), id, target.Repo)
				if verb == "create" {
					description = fmt.Sprintf("Comment on %s#%d", target.Repo, id)
				}
				if err := a.confirm(cmd, description); err != nil {
					return err
				}
			}
			var item services.Comment
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), id)
			case "create":
				item, err = service.Create(cmd.Context(), id, body)
			case "edit":
				item, err = service.Edit(cmd.Context(), id, body)
			case "delete":
				return service.Delete(cmd.Context(), id)
			}
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, commentRecord)
		}}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&body, "body", "", "Comment body")
			command.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
			editHelp := "Compose the comment in the configured editor"
			if verb == "edit" {
				editHelp = "Edit the current comment body in the configured editor"
			}
			command.Flags().BoolVar(&useEditor, "editor", false, editHelp)
		}
		if verb == "delete" {
			command.Annotations = textOutput("comment deletion does not return a resource")
		}
		group.AddCommand(command)
	}
	return group
}
