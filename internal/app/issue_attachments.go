// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// attachmentName names an attachment in messages: "attachment 9 (log.txt) on alice/x#1".
func attachmentName(t target, n int64, item services.IssueAttachment) string {
	return fmt.Sprintf("attachment %d (%s) on %s#%d", item.ID, item.Name, t.Repo, n)
}

// attachmentRow is one row of the attachment list; piped output carries exact byte counts and the URL.
func (a *application) attachmentRow(item services.IssueAttachment) []terminal.Cell {
	size := terminal.Bytes(item.Size)
	if !a.deps.Streams.OutTTY {
		size = strconv.FormatInt(item.Size, 10)
	}
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10), Link: item.URL},
		{Text: item.Name},
		{Text: size},
		{Text: strconv.FormatInt(item.DownloadCount, 10)},
		{Time: terminal.ParseTime(item.CreatedAt)},
		{Text: item.URL},
	}
}

func attachmentView(item services.IssueAttachment, d terminal.Display) terminal.View {
	return terminal.View{
		Title: item.Name, ID: "#" + strconv.FormatInt(item.ID, 10),
		Fields: []terminal.Field{
			{Label: "Size", Value: terminal.Bytes(item.Size)},
			{Label: "Downloads", Value: strconv.FormatInt(item.DownloadCount, 10)},
			timeField("Created", item.CreatedAt, d),
		},
		Bodyless: true, Kind: "attachment", URL: item.URL,
	}
}

func (a *application) issueAttachmentCommand(kind refKind) *cobra.Command {
	words := commentWords(kind)
	group := &cobra.Command{Use: "attachment", Short: "Manage the attachments of " + kind.noun() + "s", Long: fmt.Sprintf(`List, view, upload, download, rename and delete the files attached to %s. Attachments
are addressed by the %s number and the attachment ID that attachment list shows.`, kind.article(), kind.noun())}
	annotations := map[string]string{"gt.arguments": words.arguments}
	discussionTarget := func(cmd *cobra.Command, raw string) (target, int64, error) {
		t, n, err := a.resolveRef(cmd, raw, kind)
		if err == nil && kind == pullRef {
			err = requirePull(cmd, t, n)
		}
		return t, n, err
	}
	// attachmentArgs checks the attachment ID before any request.
	attachmentArgs := func(cmd *cobra.Command, args []string) (target, int64, int64, error) {
		id, err := number(args[1])
		if err != nil {
			return target{}, 0, 0, err
		}
		t, n, err := discussionTarget(cmd, args[0])
		return t, n, id, err
	}
	completeFirst := a.completeDiscussions(kind)

	var limit, page int
	list := &cobra.Command{Use: "list {<number> | <url>}", Short: "List attachments", Example: fmt.Sprintf("$ gt %s attachment list %s", words.group, words.example), Annotations: annotations, Args: argsExact(1), ValidArgsFunction: completeFirst, RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := discussionTarget(cmd, args[0])
		if err != nil {
			return err
		}
		items, err := (services.Issues{Client: t.client, Repository: t.Repo}).Attachments(cmd.Context(), n, limit, page)
		if err != nil {
			return err
		}
		where := fmt.Sprintf("on %s#%d", t.Repo, n)
		return writeTable(a, cmd, items.Items, listSpec[services.IssueAttachment]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Name", Flex: true}, {Header: "Size"}, {Header: "Downloads"}, {Header: "Created"}, {Header: "URL", PipedOnly: true}},
			Row:     a.attachmentRow, Empty: "no attachments " + where, Context: "attachments " + where,
		}, servicePage(items, page))
	}}
	pageFlags(list, &limit, &page)

	view := &cobra.Command{Use: "view {<number> | <url>} <attachment-id>", Short: "View an attachment", Example: fmt.Sprintf("$ gt %s attachment view %s 9", words.group, words.example), Annotations: annotations, Args: argsExact(2), ValidArgsFunction: completeFirst, RunE: func(cmd *cobra.Command, args []string) error {
		t, n, id, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, err := (services.Issues{Client: t.client, Repository: t.Repo}).Attachment(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		d := a.display()
		return writeView(a, cmd, item, func(item services.IssueAttachment) terminal.View { return attachmentView(item, d) })
	}}

	var uploadName string
	upload := &cobra.Command{Use: "upload {<number> | <url>} <file>", Short: "Attach a file", Example: fmt.Sprintf(`$ gt %[1]s attachment upload %[2]s crash.log
$ gt %[1]s attachment upload %[2]s screenshot.png --name before.png`, words.group, words.example), Annotations: annotations, Args: argsExact(2), ValidArgsFunction: pathArgs(1, false, completeFiles, completeFirst), RunE: func(cmd *cobra.Command, args []string) error {
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
		t, n, err := discussionTarget(cmd, args[0])
		if err != nil {
			return err
		}
		item, err := (services.Issues{Client: t.client, Repository: t.Repo}).UploadAttachment(cmd.Context(), n, name, files[0])
		if err != nil {
			return err
		}
		a.done(cmd, "Uploaded %s (%s) to %s#%d", item.Name, terminal.Bytes(item.Size), t.Repo, n)
		return a.created(cmd, item, item.URL)
	}}
	upload.Flags().StringVar(&uploadName, "name", "", "File `name` of the attachment (default: the local file name)")

	var newName string
	edit := &cobra.Command{Use: "edit {<number> | <url>} <attachment-id>", Short: "Rename an attachment", Example: fmt.Sprintf("$ gt %s attachment edit %s 9 --name crash-2026-10-11.log", words.group, words.example), Annotations: annotations, Args: argsExact(2), ValidArgsFunction: completeFirst, RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(newName) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("supply a nonempty --name"))
		}
		t, n, id, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, err := (services.Issues{Client: t.client, Repository: t.Repo}).RenameAttachment(cmd.Context(), n, id, newName)
		if err != nil {
			return err
		}
		a.done(cmd, "Renamed %s", attachmentName(t, n, item))
		return a.jsonResult(cmd, item)
	}}
	edit.Flags().StringVar(&newName, "name", "", "New file `name`")

	remove := &cobra.Command{Use: "delete {<number> | <url>} <attachment-id>", Short: "Delete an attachment", Example: fmt.Sprintf("$ gt %s attachment delete %s 9 --yes", words.group, words.example), Annotations: map[string]string{"gt.arguments": words.arguments, "gt.confirm": "true", "gt.text": "attachment deletion does not return a resource"}, Args: argsExact(2), ValidArgsFunction: completeFirst, RunE: func(cmd *cobra.Command, args []string) error {
		t, n, id, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		service := services.Issues{Client: t.client, Repository: t.Repo}
		item, err := service.Attachment(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		return a.deleteConfirmed(cmd, attachmentName(t, n, item), func() error { return service.DeleteAttachment(cmd.Context(), n, id) })
	}}

	var destination downloadFlags
	download := &cobra.Command{Use: "download {<number> | <url>} <attachment-id>", Short: "Download an attachment", Long: `Download an attachment into the current directory under its own name, or where -O/--output
or -D/--dir say. An existing file is kept unless --clobber replaces it or --skip-existing
skips the download.`, Example: fmt.Sprintf(`$ gt %[1]s attachment download %[2]s 9
$ gt %[1]s attachment download %[2]s 9 --output crash.log --clobber
# Write the file to standard output
$ gt %[1]s attachment download %[2]s 9 -O - | less`, words.group, words.example), Annotations: map[string]string{"gt.arguments": words.arguments, "gt.text": "attachment downloads are binary"}, Args: argsExact(2), ValidArgsFunction: completeFirst, RunE: func(cmd *cobra.Command, args []string) error {
		if err := destination.validate(); err != nil {
			return err
		}
		t, n, id, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, open, err := (services.Issues{Client: t.client, Repository: t.Repo}).DownloadAttachment(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		path, skip, err := destination.destination(item.Name)
		switch {
		case err != nil:
			return err
		case skip:
			a.warn(cmd, "Skipped %s: %s exists", attachmentName(t, n, item), path)
			return nil
		}
		body, err := open()
		if err != nil {
			return err
		}
		defer body.Close()
		if err := a.download(cmd, destination, path, body); err != nil {
			return err
		}
		if path != "-" {
			a.done(cmd, "Downloaded %s (%s) to %s", item.Name, terminal.Bytes(item.Size), path)
		}
		return nil
	}}
	destination.register(download)
	group.AddCommand(list, view, upload, edit, remove, download)
	return jsonOutput(group, jsonFields[services.IssueAttachment]())
}

// uploadAttachments attaches each file to the item id through upload, calls uploaded, when given,
// for each attachment, and returns an error for each file that failed.
func uploadAttachments[T any](ctx context.Context, files []*os.File, id int64, upload func(context.Context, int64, string, io.Reader) (T, error), uploaded func(T)) []error {
	var failures []error
	for _, file := range files {
		name := filepath.Base(file.Name())
		attachment, err := upload(ctx, id, name, file)
		switch {
		case err != nil:
			failures = append(failures, fmt.Errorf("attachment %s: %w", name, err))
		case uploaded != nil:
			uploaded(attachment)
		}
	}
	return failures
}

// followUpError reports the steps that failed after what was created or edited (as done says), or
// nil when none did.
func followUpError(what, done string, failures []error) error {
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("%s was %s, but follow-up steps failed: %w", what, done, errors.Join(failures...))
}
