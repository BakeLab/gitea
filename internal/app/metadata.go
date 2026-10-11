// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) labelCommand() *cobra.Command {
	group := &cobra.Command{Use: "label", Short: "Manage repository or organization labels"}
	var org string
	group.PersistentFlags().StringVar(&org, "org", "", "Manage organization labels instead of repository labels")
	service := func(cmd *cobra.Command) (services.Labels, error) {
		if org != "" && a.options.repo != "" {
			return services.Labels{}, clierr.Wrap(clierr.Usage, errors.New("--org and --repo are mutually exclusive"))
		}
		t, err := a.resolve(cmd.Context(), org == "")
		return services.Labels{Client: t.client, Repository: t.Repo, Organization: org}, err
	}
	group.AddCommand(a.labelExportCommand(service))
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List labels in the selected scope", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := service(cmd)
		if err != nil {
			return err
		}
		items, err := s.List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, labelRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"create", "edit"} {
		var name, color, description, file string
		var exclusive, archived bool
		command := &cobra.Command{Use: verb, Short: titleCase(verb) + " a label"}
		if verb == "edit" {
			command.Use += " <id>"
			command.Args = argsExact(1)
		} else {
			command.Args = argsExact(0)
		}
		command.Flags().StringVar(&name, "name", "", "Label name")
		command.Flags().StringVar(&color, "color", "", "Six-digit RGB color")
		command.Flags().StringVar(&description, "description", "", "Label description")
		command.Flags().BoolVar(&exclusive, "exclusive", false, "Only one label from this exclusive group may be applied")
		command.Flags().BoolVar(&archived, "archived", false, "Archive the label so it cannot be newly applied")
		if verb == "create" {
			command.Flags().StringVar(&file, "file", "", "Import color/name rows with optional ;description")
		}
		command.RunE = func(cmd *cobra.Command, args []string) error {
			opts := services.LabelOptions{}
			var imported []services.LabelOptions
			var id int64
			if verb == "edit" {
				var err error
				id, err = number(args[0])
				if err != nil {
					return err
				}
			}
			if file != "" {
				if cmd.Flags().Changed("name") || cmd.Flags().Changed("color") || cmd.Flags().Changed("description") || cmd.Flags().Changed("exclusive") || cmd.Flags().Changed("archived") {
					return clierr.Wrap(clierr.Usage, errors.New("--file cannot be combined with label fields"))
				}
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer f.Close()
				if imported, err = parseLabelFile(f); err != nil {
					return err
				}
			} else {
				if verb == "create" {
					if _, err := a.require(cmd, &name, "name"); err != nil {
						return err
					}
					if _, err := a.require(cmd, &color, "color"); err != nil {
						return err
					}
				}
				if verb == "create" || cmd.Flags().Changed("name") {
					if name == "" {
						return clierr.Wrap(clierr.Usage, errors.New("label name cannot be empty"))
					}
					opts.Name = &name
				}
				if verb == "create" || cmd.Flags().Changed("color") {
					color = strings.TrimPrefix(color, "#")
					if !labelColor.MatchString(color) {
						return clierr.Wrap(clierr.Usage, errors.New("--color requires six hexadecimal digits"))
					}
					opts.Color = &color
				}
				if verb == "create" || cmd.Flags().Changed("description") {
					opts.Description = &description
				}
				if verb == "create" || cmd.Flags().Changed("exclusive") {
					opts.Exclusive = &exclusive
				}
				if cmd.Flags().Changed("archived") {
					opts.Archived = &archived
				}
				if opts.Name == nil && opts.Color == nil && opts.Description == nil && opts.Exclusive == nil && opts.Archived == nil {
					return clierr.Wrap(clierr.Usage, errors.New("supply label fields to edit"))
				}
			}
			s, err := service(cmd)
			if err != nil {
				return err
			}
			if file != "" {
				results := []services.Label{}
				var failures []error
				for _, row := range imported {
					item, err := s.Create(cmd.Context(), row)
					if err != nil {
						failures = append(failures, fmt.Errorf("label %s: %w", *row.Name, err))
						continue
					}
					results = append(results, item)
				}
				return errors.Join(append(failures, writeList(a, cmd, results, labelRow))...)
			}
			var item services.Label
			if verb == "create" {
				item, err = s.Create(cmd.Context(), opts)
			} else {
				item, err = s.Edit(cmd.Context(), id, opts)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, labelRow)
		}
		group.AddCommand(command)
	}
	group.AddCommand(&cobra.Command{Use: "delete <id>", Short: "Delete a label", Annotations: textOutput("label deletion does not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		s, err := service(cmd)
		if err != nil {
			return err
		}
		if err := a.confirm(cmd, "Delete label "+args[0]); err != nil {
			return err
		}
		return s.Delete(cmd.Context(), id)
	}})
	return group
}

func (a *application) milestoneCommand() *cobra.Command {
	group := &cobra.Command{Use: "milestone", Short: "Manage repository milestones"}
	service := func(cmd *cobra.Command) (services.Milestones, error) {
		t, err := a.resolve(cmd.Context(), true)
		return services.Milestones{Client: t.client, Repository: t.Repo}, err
	}
	var limit, page int
	var state string
	list := &cobra.Command{Use: "list", Short: "List milestones", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if err := oneOf("state", state, "open", "closed", "all"); err != nil {
			return err
		}
		s, err := service(cmd)
		if err != nil {
			return err
		}
		items, err := s.List(cmd.Context(), state, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, milestoneRow)
	}}
	pageFlags(list, &limit, &page)
	list.Flags().StringVar(&state, "state", "open", "open, closed, all")
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "delete"} {
		var title, description, descriptionFile, deadline, createState string
		var byName, useEditor bool
		command := &cobra.Command{Use: verb, Short: titleCase(verb) + " a milestone"}
		if verb == "create" {
			command.Args = argsExact(0)
		} else {
			command.Use += " <id-or-name>"
			command.Flags().BoolVar(&byName, "by-name", false, "Select by exact milestone title instead of ID")
			command.Args = argsExact(1)
		}
		if verb == "create" || verb == "edit" {
			command.Flags().StringVar(&title, "title", "", "Milestone title")
			command.Flags().StringVar(&description, "description", "", "Milestone description")
			command.Flags().StringVar(&descriptionFile, "description-file", "", "Read description from file, or - for stdin")
			command.Flags().BoolVar(&useEditor, "editor", false, "Edit the milestone description in the configured editor")
			command.Flags().StringVar(&deadline, "deadline", "", "Due date: YYYY-MM-DD, RFC3339, or Unix timestamp; empty clears")
		}
		if verb == "create" {
			command.Flags().StringVar(&createState, "state", "open", "Milestone state: open or closed")
		}
		command.RunE = func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			if verb == "create" || verb == "edit" {
				if err := validateTextSources(cmd, "description", "description-file"); err != nil {
					return err
				}
			}
			opts := services.MilestoneOptions{}
			var id int64
			if verb != "create" {
				var err error
				if id, err = milestoneSelector(args[0], byName); err != nil {
					return err
				}
			}
			var interactiveDeadline *time.Time
			descriptionChanged := false
			if verb == "create" {
				if err := oneOf("state", createState, "open", "closed"); err != nil {
					return err
				}
				opts.State = &createState
				snapshot, err := a.deps.Settings.Load()
				if err != nil {
					return err
				}
				policy := a.policy(snapshot)
				if !localFlagsChanged(cmd) && policy.CanPrompt(a.deps.Streams) && a.deps.MilestoneForm != nil {
					values, err := a.deps.MilestoneForm.CreateMilestone(cmd.Context())
					if err != nil {
						return err
					}
					title, description = values.Title, values.Description
					interactiveDeadline = values.Deadline
					descriptionChanged = true
				} else if _, err := a.require(cmd, &title, "title"); err != nil {
					return err
				}
			}
			if (verb == "create" || verb == "edit") && !useEditor {
				var err error
				description, err = a.readTextSource(cmd, description, descriptionFile, "description", "description-file")
				if err != nil {
					return err
				}
				if cmd.Flags().Changed("description") || cmd.Flags().Changed("description-file") {
					descriptionChanged = true
				}
			}
			if cmd.Flags().Changed("title") && strings.TrimSpace(title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("milestone title cannot be empty"))
			}
			if verb == "create" || cmd.Flags().Changed("title") {
				opts.Title = &title
			}
			if cmd.Flags().Changed("deadline") || interactiveDeadline != nil {
				date := time.Time{}
				if interactiveDeadline != nil {
					date = *interactiveDeadline
				} else if deadline != "" {
					parsed, _, err := parseDeadline(deadline)
					if err != nil {
						return err
					}
					date = *parsed
				}
				normalized := date.Format(time.RFC3339)
				opts.Deadline = &normalized
			}

			if verb == "edit" && opts.Title == nil && !descriptionChanged && !useEditor && opts.Deadline == nil {
				return clierr.Wrap(clierr.Usage, errors.New("supply milestone fields to edit"))
			}
			s, err := service(cmd)
			if err != nil {
				return err
			}
			if byName {
				id, err = s.ResolveName(cmd.Context(), args[0])
				if err != nil {
					return err
				}
			}
			if verb == "delete" {
				if err := a.confirm(cmd, "Delete milestone "+args[0]); err != nil {
					return err
				}
				if a.options.json != "" {
					item, err := s.View(cmd.Context(), id)
					if err != nil {
						return err
					}
					if err := s.Delete(cmd.Context(), id); err != nil {
						return err
					}
					return writeItem(a, cmd, item, milestoneRow)
				}
				return s.Delete(cmd.Context(), id)
			}
			if useEditor {
				initial := ""
				if verb == "edit" {
					current, viewErr := s.View(cmd.Context(), id)
					if viewErr != nil {
						return viewErr
					}
					initial = current.Description
				}
				editorDraft, err = a.editDraft(cmd, initial)
				if err != nil {
					return err
				}
				description = editorDraft.Body
				descriptionChanged = true
			}
			if verb == "create" || descriptionChanged {
				opts.Description = &description
			}
			var item services.Milestone
			if verb == "view" {
				item, err = s.View(cmd.Context(), id)
			} else if verb == "create" {
				item, err = s.Create(cmd.Context(), opts)
			} else {
				item, err = s.Edit(cmd.Context(), id, opts)
			}
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, milestoneRow)
		}
		group.AddCommand(command)
	}
	group.AddCommand(a.milestoneStateCommand("close", service), a.milestoneStateCommand("reopen", service))
	group.AddCommand(a.milestoneIssueCommand())
	return group
}

func labelRow(item services.Label) []terminal.Cell {
	return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10)}, {Text: item.Name, Color: item.Color}, {Text: item.Color, Color: item.Color}, {Text: item.Description}}
}

func milestoneRow(item services.Milestone) []terminal.Cell {
	return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10)}, {Text: item.Title}, {Text: item.State, Color: stateColors[item.State]}}
}
