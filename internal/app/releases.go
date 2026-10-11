// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func releaseRecord(item services.Release) terminal.Record {
	return terminal.Record{Heading: terminal.Row(item.Tag, item.Title), Lines: []string{item.URL}}
}

func releaseRow(item services.Release) []terminal.Cell {
	status := "released"
	switch {
	case item.Draft:
		status = "draft"
	case item.Prerelease:
		status = "prerelease"
	}
	return []terminal.Cell{{Text: item.Tag, Link: item.URL}, {Text: item.Title}, {Text: status}}
}

func (a *application) releaseCommand() *cobra.Command {
	group := &cobra.Command{Use: "release", Short: "Manage repository releases"}
	var limit, page int
	list := &cobra.Command{Use: "list", Short: "List releases", Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Releases{Client: target.client, Repository: target.Repo}).List(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, releaseRow)
	}}
	pageFlags(list, &limit, &page)
	group.AddCommand(list)
	for _, verb := range []string{"view", "create", "edit", "publish", "delete"} {
		var tag, title, body, file, targetName string
		var draft, prerelease, deleteTag, useEditor bool
		var assetPaths []string
		command := &cobra.Command{Use: verb + " <tag>", Short: titleCase(verb) + " repository releases", Args: argsExact(1)}
		if verb == "edit" || verb == "delete" || verb == "publish" {
			command.Use = verb + " <tag>..."
			command.Args = usageArgs(cobra.MinimumNArgs(1))
		}
		if verb == "create" {
			command.Use = "create [tag]"
			command.Args = usageArgs(cobra.MaximumNArgs(1))
			command.Flags().StringVar(&tag, "tag", "", "Release tag")
			command.Flags().StringSliceVar(&assetPaths, "asset", nil, "Asset file to upload after creation (repeatable)")
		}
		if verb == "create" || verb == "edit" {
			if verb == "edit" {
				command.Flags().StringVar(&tag, "tag", "", "Change the release tag (one release only)")
			}
			command.Flags().StringVar(&title, "title", "", "Release title (creation defaults to tag)")
			command.Flags().BoolVar(&useEditor, "editor", false, "Edit release notes in an editor")
			command.Flags().StringVar(&body, "body", "", "Release notes")
			command.Flags().StringVar(&file, "body-file", "", "Read notes from file, or - for stdin")
			command.Flags().StringVar(&targetName, "target", "", "Target branch or commit for the tag")
			command.Flags().BoolVar(&draft, "draft", false, "Draft release")
			command.Flags().BoolVar(&prerelease, "prerelease", false, "Prerelease")
		}
		if verb == "delete" {
			command.Flags().BoolVar(&deleteTag, "delete-tag", false, "Delete the associated Git tag too")
		}
		command.RunE = func(cmd *cobra.Command, args []string) (runErr error) {
			var editorDraft terminal.Draft
			defer finishDraft(&editorDraft, &runErr)
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			if useEditor && verb == "edit" && len(args) != 1 {
				return clierr.Wrap(clierr.Usage, errors.New("--editor requires one release"))
			}
			opts := services.ReleaseOptions{}
			files, err := openRegularFiles(assetPaths)
			defer closeFiles(files)
			if err != nil {
				return err
			}
			prompted := false
			if verb == "create" {
				if len(args) > 0 {
					if cmd.Flags().Changed("tag") {
						return clierr.Wrap(clierr.Usage, errors.New("supply a tag argument or --tag, not both"))
					}
					tag = args[0]
				}
				if tag == "" && file == "-" {
					return clierr.Wrap(clierr.Usage, errors.New("--body-file - requires a tag; stdin cannot also supply a prompt"))
				}
				prompted, err = a.require(cmd, &tag, "tag")
				if err != nil {
					return err
				}
				if title == "" && !cmd.Flags().Changed("title") {
					title = tag
				}
				opts.Tag = &tag
				opts.Title = &title
				opts.Draft = &draft
				opts.Prerelease = &prerelease
			}
			if verb == "create" || verb == "edit" {
				if verb == "edit" && cmd.Flags().Changed("tag") {
					if len(args) != 1 || strings.TrimSpace(tag) == "" {
						return clierr.Wrap(clierr.Usage, errors.New("--tag requires one release and a nonempty new tag"))
					}
					opts.Tag = &tag
				}
				if cmd.Flags().Changed("title") {
					if strings.TrimSpace(title) == "" {
						return clierr.Wrap(clierr.Usage, errors.New("release title cannot be empty"))
					}
					opts.Title = &title
				}
				if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
					var err error
					body, err = a.readBody(cmd, body, file)
					if err != nil {
						return err
					}
					opts.Body = &body
				}
				if cmd.Flags().Changed("target") {
					opts.Target = &targetName
				}
				if cmd.Flags().Changed("draft") {
					opts.Draft = &draft
				}
				if cmd.Flags().Changed("prerelease") {
					opts.Prerelease = &prerelease
				}
				if verb == "edit" && !useEditor && opts == (services.ReleaseOptions{}) {
					return clierr.Wrap(clierr.Usage, errors.New("supply release fields to edit"))
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Releases{Client: target.client, Repository: target.Repo}
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
				opts.Body = &editorDraft.Body
			}
			if verb == "delete" || prompted {
				subject := strings.Join(args, " ")
				if verb == "create" {
					subject = tag
				}
				if err := a.confirm(cmd, fmt.Sprintf("%s release %s on %s", verb, subject, target.Repo)); err != nil {
					return err
				}
			}
			if verb == "create" {
				item, err := service.Create(cmd.Context(), opts)
				if err != nil {
					return err
				}
				var failures []error
				for _, file := range files {
					if _, err := service.UploadAsset(cmd.Context(), item.ID, filepath.Base(file.Name()), file); err != nil {
						failures = append(failures, fmt.Errorf("release %s created; asset %s upload failed: %w", item.Tag, filepath.Base(file.Name()), err))
					}
				}
				if err := writeRecord(a, cmd, item, releaseRecord); err != nil {
					return err
				}
				return errors.Join(failures...)
			}
			if verb == "view" {
				item, err := service.View(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				return writeRecord(a, cmd, item, releaseRecord)
			}
			return batch(a, cmd, args, func(name string) string { return "release " + name }, func(name string) (services.Release, error) {
				item, err := service.View(cmd.Context(), name)
				if err == nil {
					switch verb {
					case "edit":
						item, err = service.Edit(cmd.Context(), item.ID, opts)
					case "publish":
						published := false
						item, err = service.Edit(cmd.Context(), item.ID, services.ReleaseOptions{Draft: &published})
					case "delete":
						err = service.Delete(cmd.Context(), item, deleteTag)
					}
				}
				return item, err
			}, releaseRow)
		}
		group.AddCommand(command)
	}
	group.AddCommand(a.releaseAssetCommand())
	return group
}
