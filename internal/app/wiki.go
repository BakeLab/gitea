// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"math"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func wikiRecord(item services.WikiPage) terminal.Record {
	return terminal.Record{Heading: []terminal.Cell{{Text: item.Title, Bold: true}}, Lines: []string{item.Body, item.URL}}
}

func (a *application) wikiCommand() *cobra.Command {
	group := &cobra.Command{Use: "wiki", Short: "Manage repository wiki pages"}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List wiki pages", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Wiki{Client: target.client, Repository: target.Repo}).List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, func(item services.WikiPage) []terminal.Cell {
			return terminal.Row(item.Title, item.Author, item.UpdatedAt)
		})
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	var revisionPage int
	revisions := &cobra.Command{Use: "revisions <page>", Short: "List revisions of a wiki page", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Wiki{Client: target.client, Repository: target.Repo}).Revisions(cmd.Context(), args[0], revisionPage)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, func(item services.WikiRevision) []terminal.Cell {
			return terminal.Row(item.SHA, item.Author, item.CreatedAt, item.Message)
		})
	}}
	positiveIntVar(revisions, &revisionPage, "page", 1, math.MaxInt, "Revision page (server controls page size)")
	group.AddCommand(revisions)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var title, body, file, message string
		var useEditor bool
		command := &cobra.Command{Use: verb + " <page>", Short: titleCase(verb) + " a wiki page", Args: argsExact(1)}
		switch verb {
		case "create":
			command.Use = verb
			command.Args = argsExact(0)
		case "delete":
			command.Annotations = textOutput("wiki page deletion does not return a resource")
		}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&title, "title", "", "Page title")
			command.Flags().BoolVar(&useEditor, "editor", false, "Edit page content in an editor")
			command.Flags().StringVar(&body, "body", "", "Page content")
			command.Flags().StringVar(&file, "body-file", "", "Read content from file, or - for stdin")
			command.Flags().StringVar(&message, "message", "", "Wiki commit message")
		}
		command.RunE = func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			prompted := false
			if verb == "create" || verb == "edit" {
				if file == "-" && verb == "create" && title == "" {
					return clierr.Wrap(clierr.Usage, errors.New("--body-file - requires --title; stdin cannot also supply a prompt"))
				}
				var err error
				if body, err = a.readBody(cmd, body, file); err != nil {
					return err
				}
				if verb == "create" {
					prompted, err = a.require(cmd, &title, "title")
					if err != nil {
						return err
					}
				}
				if !useEditor && !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
					asked, err := a.require(cmd, &body, "body")
					if err != nil {
						return err
					}
					prompted = prompted || asked
				}
				if !useEditor && strings.TrimSpace(body) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("wiki content cannot be empty"))
				}
				if (verb == "create" || cmd.Flags().Changed("title")) && strings.TrimSpace(title) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("wiki title cannot be empty"))
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Wiki{Client: target.client, Repository: target.Repo}
			if useEditor {
				initial := ""
				if verb == "edit" {
					item, err := service.View(cmd.Context(), args[0])
					if err != nil {
						return err
					}
					initial = item.Body
				}
				editorDraft, err = a.editDraft(cmd, initial)
				if err != nil {
					return err
				}
				body = editorDraft.Body
				if strings.TrimSpace(body) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("wiki content cannot be empty"))
				}
			}
			if verb == "delete" || prompted {
				subject := title
				if verb != "create" {
					subject = args[0]
				}
				if err := a.confirm(cmd, verb+" wiki page "+subject+" on "+target.Repo); err != nil {
					return err
				}
			}
			var item services.WikiPage
			switch verb {
			case "view":
				item, err = service.View(cmd.Context(), args[0])
			case "create":
				item, err = service.Create(cmd.Context(), services.WikiOptions{Title: title, Body: body, Message: message})
			case "edit":
				item, err = service.Edit(cmd.Context(), args[0], services.WikiOptions{Title: title, Body: body, Message: message})
			case "delete":
				return service.Delete(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, wikiRecord)
		}
		group.AddCommand(command)
	}
	return group
}
