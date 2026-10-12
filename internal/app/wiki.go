// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) wikiCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "wiki",
		Short: "Manage repository wiki pages",
		Long:  "List, view, create, edit and delete the pages of a repository's wiki, and list their revisions.",
	}
	group.AddCommand(a.wikiListCommand(), a.wikiViewCommand(), a.wikiRevisionsCommand(), a.wikiCreateCommand(), a.wikiEditCommand(), a.wikiDeleteCommand())
	return group
}

// completeWikiPages completes the titles of the repository's wiki pages.
func (a *application) completeWikiPages() cobra.CompletionFunc {
	return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		pages, err := services.Wiki{Client: t.client, Repository: t.Repo}.ListPage(ctx, completionLimit, 1)
		return describe(pages.Items, func(page services.WikiPage) (string, string) { return page.Title, "" }), err
	})
}

func (a *application) wikiListCommand() *cobra.Command {
	var limit, page int
	command := &cobra.Command{
		Use:     "list",
		Short:   "List wiki pages",
		Example: "$ gt wiki list\n$ gt wiki list -R alice/project --json title,url",
		Args:    argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			result, err := (services.Wiki{Client: target.client, Repository: target.Repo}).ListPage(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			spec := listSpec[services.WikiPage]{
				Columns: []terminal.Column{{Header: "Title", Flex: true}, {Header: "Author"}, {Header: "Updated"}, {Header: "URL", PipedOnly: true}},
				Row: func(item services.WikiPage) []terminal.Cell {
					return []terminal.Cell{{Text: item.Title, Link: item.URL}, {Text: item.Author}, {Time: terminal.ParseTime(item.UpdatedAt)}, {Text: item.URL}}
				},
				Context: "wiki pages in " + target.Repo, Empty: "no wiki pages in " + target.Repo,
			}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, page))
		},
	}
	pageFlags(command, &limit, &page)
	return command
}

func (a *application) wikiViewCommand() *cobra.Command {
	var web bool
	command := &cobra.Command{
		Use:     "view <page>",
		Short:   "View a wiki page",
		Long:    "View a wiki page, rendered on a terminal.",
		Example: "$ gt wiki view Home\n$ gt wiki view \"Getting Started\" --web",
		Args:    argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			item, err := (services.Wiki{Client: target.client, Repository: target.Repo}).View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if web {
				return a.openBrowser(cmd, item.URL)
			}
			return writeView(a, cmd, item, a.wikiView)
		},
	}
	command.Flags().BoolVarP(&web, "web", "w", false, "Open the page in the browser")
	command.ValidArgsFunction = a.completeWikiPages()
	return command
}

// wikiView shows a wiki page with its content as the body.
func (a *application) wikiView(item services.WikiPage) terminal.View {
	var meta []string
	if item.Author != "" {
		meta = append(meta, item.Author)
	}
	if updated := terminal.ParseTime(item.UpdatedAt); !updated.IsZero() {
		meta = append(meta, "updated "+terminal.RelativeTime(a.deps.Now(), updated))
	}
	if item.CommitCount > 0 {
		meta = append(meta, terminal.Plural(int(item.CommitCount), "revision"))
	}
	return terminal.View{
		Title: item.Title, Meta: strings.Join(meta, " · "), Body: item.Body, Kind: "wiki page", URL: item.URL,
		Fields: []terminal.Field{{Label: "Author", Value: item.Author, PipedOnly: true}, {Label: "Updated", Value: item.UpdatedAt, PipedOnly: true}},
	}
}

func (a *application) wikiRevisionsCommand() *cobra.Command {
	var page int
	command := &cobra.Command{
		Use:     "revisions <page>",
		Short:   "List revisions of a wiki page",
		Example: "$ gt wiki revisions Home",
		Args:    argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			items, err := (services.Wiki{Client: target.client, Repository: target.Repo}).Revisions(cmd.Context(), args[0], page)
			if err != nil {
				return err
			}
			spec := listSpec[services.WikiRevision]{
				Columns: []terminal.Column{{Header: "Commit"}, {Header: "Author"}, {Header: "Date"}, {Header: "Message", Flex: true}},
				Row: func(item services.WikiRevision) []terminal.Cell {
					message, _, _ := strings.Cut(item.Message, "\n")
					sha := item.SHA
					if a.deps.Streams.OutTTY {
						sha = shortSHA(sha)
					}
					return []terminal.Cell{{Text: sha, Muted: true}, {Text: item.Author}, {Time: terminal.ParseTime(item.CreatedAt)}, {Text: message}}
				},
				Context: "revisions of wiki page " + strconv.Quote(args[0]), Empty: "no revisions of wiki page " + strconv.Quote(args[0]),
			}
			return writeTable(a, cmd, items, spec, listPage{Number: page, Counted: false, More: false})
		},
	}
	positiveIntVar(command, &page, "page", 1, math.MaxInt, "Revision page (server controls page size)")
	command.ValidArgsFunction = a.completeWikiPages()
	return command
}

// wikiContent are the flags of wiki create and wiki edit.
type wikiContent struct {
	title, body, file, message string
	editor                     bool
}

func (c *wikiContent) register(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&c.title, "title", "t", "", "Page title")
	flags.BoolVarP(&c.editor, "editor", "e", false, "Write the page content in an editor")
	flags.StringVarP(&c.body, "body", "b", "", "Page content")
	flags.StringVarP(&c.file, "body-file", "F", "", "Read the page content from a file, or - for standard input")
	flags.StringVarP(&c.message, "message", "m", "", "Commit message of the change")
}

func (a *application) wikiCreateCommand() *cobra.Command {
	var content wikiContent
	command := &cobra.Command{
		Use:   "create [<title>]",
		Short: "Create a wiki page",
		Long:  "Create a wiki page. Without --body or --body-file, the content is written in an editor or typed on the terminal.",
		Example: `$ gt wiki create "Getting Started" --body-file docs/start.md
$ gt wiki create Home --editor`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var draft terminal.Draft
			defer finishDraft(&draft, &runErr)
			if len(args) > 0 {
				if cmd.Flags().Changed("title") {
					return clierr.Wrap(clierr.Usage, errors.New("supply the title as an argument or --title, not both"))
				}
				content.title = args[0]
			}
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			if content.file == "-" && content.title == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--body-file - requires a title; stdin cannot also supply a prompt"))
			}
			body, err := a.readBody(cmd, content.body, content.file)
			if err != nil {
				return err
			}
			if _, err := a.requireInput(cmd, &content.title, "title", "Page title", nil); err != nil {
				return err
			}
			if strings.TrimSpace(content.title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("wiki title cannot be empty"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
				if draft, err = a.wikiBody(cmd, content.editor, ""); err != nil {
					return err
				}
				body = draft.Body
			}
			if strings.TrimSpace(body) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("wiki content cannot be empty"))
			}
			item, err := (services.Wiki{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), services.WikiOptions{Title: content.title, Body: body, Message: content.message})
			if err != nil {
				return err
			}
			a.done(cmd, "Created wiki page %q in %s", item.Title, target.Repo)
			return a.created(cmd, item, item.URL)
		},
	}
	content.register(command)
	return command
}

// wikiBody collects page content: in the editor with --editor, else in the editor or an inline
// field on a terminal. Without a terminal it fails, naming --body.
func (a *application) wikiBody(cmd *cobra.Command, editor bool, initial string) (terminal.Draft, error) {
	if editor {
		return a.editDraft(cmd, initial)
	}
	if !a.canPrompt() {
		return terminal.Draft{}, clierr.Wrap(clierr.Usage, errors.New("missing required input: supply --body, --body-file or --editor"))
	}
	return a.bodyEditor(cmd)(cmd.Context(), initial)
}

func (a *application) wikiEditCommand() *cobra.Command {
	var content wikiContent
	command := &cobra.Command{
		Use:   "edit <page>",
		Short: "Edit a wiki page",
		Long:  "Change the title or the content of a wiki page. Renaming with --title alone keeps the content.",
		Example: `$ gt wiki edit Home --body-file docs/home.md --message "Rewrite the home page"
$ gt wiki edit "Old Title" --title "New Title"
$ gt wiki edit Home --editor`,
		Args: argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var draft terminal.Draft
			defer finishDraft(&draft, &runErr)
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			if !changed("title") && !changed("body") && !changed("body-file") && !content.editor {
				return clierr.Wrap(clierr.Usage, errors.New("supply --title, --body, --body-file or --editor"))
			}
			if changed("title") && strings.TrimSpace(content.title) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("wiki title cannot be empty"))
			}
			body, err := a.readBody(cmd, content.body, content.file)
			if err != nil {
				return err
			}
			if (changed("body") || changed("body-file")) && strings.TrimSpace(body) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("wiki content cannot be empty"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Wiki{Client: target.client, Repository: target.Repo}
			if content.editor || (!changed("body") && !changed("body-file")) {
				current, err := service.View(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				body = current.Body
				if content.editor {
					if draft, err = a.editDraft(cmd, current.Body); err != nil {
						return err
					}
					if body = draft.Body; strings.TrimSpace(body) == "" {
						return clierr.Wrap(clierr.Usage, errors.New("wiki content cannot be empty"))
					}
				}
			}
			item, err := service.Edit(cmd.Context(), args[0], services.WikiOptions{Title: content.title, Body: body, Message: content.message})
			if err != nil {
				return err
			}
			if item.Title != args[0] {
				a.done(cmd, "Renamed wiki page %q to %q in %s", args[0], item.Title, target.Repo)
			} else {
				a.done(cmd, "Edited wiki page %q in %s", item.Title, target.Repo)
			}
			return a.jsonResult(cmd, item)
		},
	}
	content.register(command)
	command.ValidArgsFunction = a.completeWikiPages()
	return command
}

func (a *application) wikiDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:         "delete <page>",
		Short:       "Delete a wiki page",
		Example:     "$ gt wiki delete \"Old Notes\" --yes",
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "wiki page deletion does not return a resource"},
		Args:        argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			subject := "wiki page " + strconv.Quote(args[0]) + " from " + target.Repo
			return a.deleteConfirmed(cmd, subject, func() error {
				return (services.Wiki{Client: target.client, Repository: target.Repo}).Delete(cmd.Context(), args[0])
			})
		},
	}
	command.ValidArgsFunction = a.completeWikiPages()
	return command
}
