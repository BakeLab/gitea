// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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
func attachmentName(item services.IssueAttachment, where string) string {
	return fmt.Sprintf("attachment %d (%s) on %s", item.ID, item.Name, where)
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

// attachmentStore is the attachment collection of an issue or pull request, keyed by its number,
// or of a comment, keyed by its ID.
type attachmentStore interface {
	Attachments(ctx context.Context, owner int64, limit, page int) (services.Page[services.IssueAttachment], error)
	Attachment(ctx context.Context, owner, id int64) (services.IssueAttachment, error)
	UploadAttachment(ctx context.Context, owner int64, name string, input io.Reader) (services.IssueAttachment, error)
	RenameAttachment(ctx context.Context, owner, id int64, name string) (services.IssueAttachment, error)
	DeleteAttachment(ctx context.Context, owner, id int64) error
	DownloadAttachment(ctx context.Context, owner, id int64) (services.IssueAttachment, func() (io.ReadCloser, error), error)
}

// attachmentParent is what an attachment command group attaches files to.
type attachmentParent struct {
	short, long, operand, noun, group, example string
	annotations                                map[string]string
	complete                                   cobra.CompletionFunc
	// resolve returns the repository, the key the store takes and the name messages use.
	resolve func(cmd *cobra.Command, raw string) (target, int64, string, error)
	store   func(t target) attachmentStore
}

func (a *application) issueAttachmentCommand(kind refKind) *cobra.Command {
	words := commentWords(kind)
	return a.attachmentCommand(attachmentParent{
		short: "Manage the attachments of " + kind.noun() + "s", long: fmt.Sprintf(`List, view, upload, download, rename and delete the files attached to %s. Attachments
are addressed by the %s number and the attachment ID that attachment list shows.`, kind.article(), kind.noun()),
		operand: "{<number> | <url>}", noun: kind.noun(), group: words.group + " attachment", example: words.example,
		annotations: map[string]string{"gt.arguments": words.arguments}, complete: a.completeDiscussions(kind),
		resolve: func(cmd *cobra.Command, raw string) (target, int64, string, error) {
			t, n, err := a.resolveRef(cmd, raw, kind)
			if err == nil && kind == pullRef {
				err = requirePull(cmd, t, n)
			}
			return t, n, fmt.Sprintf("%s#%d", t.Repo, n), err
		},
		store: func(t target) attachmentStore { return services.Issues{Client: t.client, Repository: t.Repo} },
	})
}

func (a *application) commentAttachmentCommand(kind refKind) *cobra.Command {
	return a.attachmentCommand(attachmentParent{
		short: "Manage the attachments of comments", long: `List, view, upload, download, rename and delete the files attached to a comment. Attachments
are addressed by the comment ID that comment list shows and the attachment ID that attachment
list shows.`,
		operand: "<comment-id>", noun: "comment ID", group: commentWords(kind).group + " comment attachment", example: "81", complete: cobra.NoFileCompletions,
		resolve: func(cmd *cobra.Command, raw string) (target, int64, string, error) {
			// An inline review comment cannot be fetched, but its attachments can.
			t, comment, err := a.comment(cmd, raw, kind, true)
			return t, comment.ID, commentName(t, comment), err
		},
		store: func(t target) attachmentStore { return services.Comments{Client: t.client, Repository: t.Repo} },
	})
}

func (a *application) attachmentCommand(parent attachmentParent) *cobra.Command {
	group := &cobra.Command{Use: "attachment", Short: parent.short, Long: parent.long}
	operand := parent.operand
	example := func(subcommand, rest string) string {
		return fmt.Sprintf("$ gt %s %s %s%s", parent.group, subcommand, parent.example, rest)
	}
	annotations := func(extra map[string]string) map[string]string {
		result := maps.Clone(parent.annotations)
		if result == nil {
			result = map[string]string{}
		}
		maps.Copy(result, extra)
		return result
	}
	// attachmentArgs checks the attachment ID before any request.
	attachmentArgs := func(cmd *cobra.Command, args []string) (target, int64, int64, string, error) {
		id, err := number(args[1])
		if err != nil {
			return target{}, 0, 0, "", err
		}
		t, owner, where, err := parent.resolve(cmd, args[0])
		return t, owner, id, where, err
	}

	var limit, page int
	list := &cobra.Command{Use: "list " + operand, Short: "List attachments", Example: example("list", ""), Annotations: annotations(nil), Args: argsExact(1), ValidArgsFunction: parent.complete, RunE: func(cmd *cobra.Command, args []string) error {
		t, owner, where, err := parent.resolve(cmd, args[0])
		if err != nil {
			return err
		}
		items, err := parent.store(t).Attachments(cmd.Context(), owner, limit, page)
		if err != nil {
			return err
		}
		return writeTable(a, cmd, items.Items, listSpec[services.IssueAttachment]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Name", Flex: true}, {Header: "Size"}, {Header: "Downloads"}, {Header: "Created"}, {Header: "URL", PipedOnly: true}},
			Row:     a.attachmentRow, Empty: "no attachments on " + where, Context: "attachments on " + where,
		}, servicePage(items, page))
	}}
	pageFlags(list, &limit, &page)

	view := &cobra.Command{Use: "view " + operand + " <attachment-id>", Short: "View an attachment", Example: example("view", " 9"), Annotations: annotations(nil), Args: argsExact(2), ValidArgsFunction: parent.complete, RunE: func(cmd *cobra.Command, args []string) error {
		t, owner, id, _, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, err := parent.store(t).Attachment(cmd.Context(), owner, id)
		if err != nil {
			return err
		}
		d := a.display()
		return writeView(a, cmd, item, func(item services.IssueAttachment) terminal.View { return attachmentView(item, d) })
	}}

	var uploadName string
	upload := &cobra.Command{Use: "upload " + operand + " <file>", Short: "Attach a file", Example: fmt.Sprintf(`$ gt %[1]s upload %[2]s crash.log
$ gt %[1]s upload %[2]s screenshot.png --name before.png`, parent.group, parent.example), Annotations: annotations(nil), Args: nonBlankArgs(cobra.ExactArgs(2), parent.noun, "file"), ValidArgsFunction: pathArgs(1, false, completeFiles, parent.complete), RunE: func(cmd *cobra.Command, args []string) error {
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
		t, owner, where, err := parent.resolve(cmd, args[0])
		if err != nil {
			return err
		}
		item, err := parent.store(t).UploadAttachment(cmd.Context(), owner, name, files[0])
		if err != nil {
			return err
		}
		a.done(cmd, "Uploaded %s (%s) to %s", item.Name, terminal.Bytes(item.Size), where)
		return a.created(cmd, item, item.URL)
	}}
	upload.Flags().StringVar(&uploadName, "name", "", "File `name` of the attachment (default: the local file name)")

	var newName string
	edit := &cobra.Command{Use: "edit " + operand + " <attachment-id>", Short: "Rename an attachment", Example: example("edit", " 9 --name crash-2026-10-11.log"), Annotations: annotations(nil), Args: argsExact(2), ValidArgsFunction: parent.complete, RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(newName) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("supply a nonempty --name"))
		}
		t, owner, id, where, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, err := parent.store(t).RenameAttachment(cmd.Context(), owner, id, newName)
		if err != nil {
			return err
		}
		a.done(cmd, "Renamed %s", attachmentName(item, where))
		return a.jsonResult(cmd, item)
	}}
	edit.Flags().StringVar(&newName, "name", "", "New file `name`")

	remove := &cobra.Command{Use: "delete " + operand + " <attachment-id>", Short: "Delete an attachment", Example: example("delete", " 9 --yes"), Annotations: annotations(map[string]string{"gt.confirm": "true", "gt.text": "attachment deletion does not return a resource"}), Args: argsExact(2), ValidArgsFunction: parent.complete, RunE: func(cmd *cobra.Command, args []string) error {
		t, owner, id, where, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		store := parent.store(t)
		item, err := store.Attachment(cmd.Context(), owner, id)
		if err != nil {
			return err
		}
		return a.deleteConfirmed(cmd, attachmentName(item, where), func() error { return store.DeleteAttachment(cmd.Context(), owner, id) })
	}}

	var destination downloadFlags
	download := &cobra.Command{Use: "download " + operand + " <attachment-id>", Short: "Download an attachment", Long: `Download an attachment into the current directory under its own name, or where -O/--output
or -D/--dir say. An existing file is kept unless --clobber replaces it or --skip-existing
skips the download.`, Example: fmt.Sprintf(`$ gt %[1]s download %[2]s 9
$ gt %[1]s download %[2]s 9 --output crash.log --clobber
# Write the file to standard output
$ gt %[1]s download %[2]s 9 -O - | less`, parent.group, parent.example), Annotations: annotations(map[string]string{"gt.text": "attachment downloads are binary"}), Args: argsExact(2), ValidArgsFunction: parent.complete, RunE: func(cmd *cobra.Command, args []string) error {
		if err := destination.validate(); err != nil {
			return err
		}
		t, owner, id, where, err := attachmentArgs(cmd, args)
		if err != nil {
			return err
		}
		item, open, err := parent.store(t).DownloadAttachment(cmd.Context(), owner, id)
		if err != nil {
			return err
		}
		path, skip, err := destination.destination(item.Name)
		switch {
		case err != nil:
			return err
		case skip:
			a.warn(cmd, "Skipped %s: %s exists", attachmentName(item, where), path)
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
	return jsonOutput[services.IssueAttachment](group)
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
