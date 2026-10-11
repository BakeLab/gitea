// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) issueAttachmentCommand() *cobra.Command {
	group := &cobra.Command{Use: "attachment", Short: "Inspect issue or pull request attachments"}
	var limit, page int
	list := &cobra.Command{Use: "list <number>", Short: "List attachments", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		items, err := (services.Issues{Client: target.client, Repository: target.Repo}).Attachments(cmd.Context(), n, limit, page)
		if err != nil {
			return err
		}
		return writeList(a, cmd, items, attachmentRow)
	}}
	pageFlags(list, &limit, &page)
	view := &cobra.Command{Use: "view <number> <attachment-id>", Short: "View attachment metadata", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		id, err := number(args[1])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Issues{Client: target.client, Repository: target.Repo}).Attachment(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, attachmentRow)
	}}
	for _, verb := range []string{"edit", "delete"} {
		var name string
		command := &cobra.Command{Use: verb + " <number> <attachment-id>", Short: titleCase(verb) + " an attachment", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			id, err := number(args[1])
			if err != nil {
				return err
			}
			if verb == "edit" && strings.TrimSpace(name) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("supply a nonempty --name"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			if verb == "delete" {
				if err := a.confirm(cmd, fmt.Sprintf("Delete attachment %d on %s#%d", id, target.Repo, n)); err != nil {
					return err
				}
				return service.DeleteAttachment(cmd.Context(), n, id)
			}
			item, err := service.RenameAttachment(cmd.Context(), n, id, name)
			if err != nil {
				return err
			}
			return writeItem(a, cmd, item, attachmentRow)
		}}
		if verb == "edit" {
			command.Flags().StringVar(&name, "name", "", "New attachment filename")
		}
		if verb == "delete" {
			command.Annotations = textOutput("attachment deletion does not return a resource")
		}
		group.AddCommand(command)
	}
	var uploadName string
	upload := &cobra.Command{Use: "upload <number> <file>", Short: "Upload one attachment", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		name := uploadName
		if cmd.Flags().Changed("name") && strings.TrimSpace(name) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--name cannot be empty"))
		}
		if name == "" {
			name = filepath.Base(args[1])
		}
		files, err := openRegularFiles(args[1:])
		defer closeFiles(files)
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Issues{Client: target.client, Repository: target.Repo}).UploadAttachment(cmd.Context(), n, name, files[0])
		if err != nil {
			return err
		}
		return writeItem(a, cmd, item, attachmentRow)
	}}
	upload.Flags().StringVar(&uploadName, "name", "", "Uploaded filename (default: local filename)")
	var output string
	download := &cobra.Command{Use: "download <number> <attachment-id>", Short: "Download an attachment to a file or stdout", Annotations: textOutput("attachment downloads are binary"), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		id, err := number(args[1])
		if err != nil {
			return err
		}
		if err := validateDownloadDestination(output); err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		body, err := (services.Issues{Client: target.client, Repository: target.Repo}).DownloadAttachment(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		defer body.Close()
		return writeDownload(cmd, body, output)
	}}
	download.Flags().StringVar(&output, "output-file", "", "Destination file (must not exist), or - for stdout")
	group.AddCommand(list, view, upload, download)
	return group
}

func attachmentRow(item services.IssueAttachment) []terminal.Cell {
	return terminal.Row(strconv.FormatInt(item.ID, 10), item.Name, strconv.FormatInt(item.Size, 10), item.URL)
}
