// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

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
	return jsonOutput(group, jsonFields[services.WikiPage]())
}

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
	// Gitea lists pages without their content.
	return jsonOutput(command, slices.DeleteFunc(jsonFields[services.WikiPage](), func(field string) bool {
		return slices.Contains([]string{"body", "commit_count", "footer", "sidebar"}, field)
	}))
}

func (a *application) wikiViewCommand() *cobra.Command {
	var web bool
	command := &cobra.Command{
		Use:     "view <page>",
		Short:   "View a wiki page",
		Long:    "View a wiki page, rendered on a terminal.",
		Example: "$ gt wiki view Home\n$ gt wiki view \"Getting Started\" --web",
		Args:    nonBlankArgs(cobra.ExactArgs(1), "page name"),
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
		Fields: []terminal.Field{{Label: "Author", Value: item.Author, PipedOnly: true}, {Label: "Updated", Value: pipedTime(item.UpdatedAt), PipedOnly: true}},
	}
}

func (a *application) wikiRevisionsCommand() *cobra.Command {
	var page int
	command := &cobra.Command{
		Use:     "revisions <page>",
		Short:   "List revisions of a wiki page",
		Example: "$ gt wiki revisions Home",
		Args:    nonBlankArgs(cobra.ExactArgs(1), "page name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			revisions, err := (services.Wiki{Client: target.client, Repository: target.Repo}).Revisions(cmd.Context(), args[0], page)
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
			return writeTable(a, cmd, revisions.Items, spec, servicePage(revisions, page))
		},
	}
	positiveIntVar(command, &page, "page", 1, math.MaxInt, "Revision page (server controls page size)")
	command.ValidArgsFunction = a.completeWikiPages()
	return jsonOutput(command, jsonFields[services.WikiRevision]())
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
		Args: nonBlankArgs(cobra.MaximumNArgs(1), "page title"),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var draft terminal.Draft
			defer finishDraft(&draft, &runErr)
			if len(args) > 0 {
				if cmd.Flags().Changed("title") {
					return clierr.Wrap(clierr.Usage, errors.New("a title argument cannot be combined with --title"))
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
			if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Reason == services.ReasonExists && conflict.Hint == "" {
				conflict.Hint = "edit it with: " + content.editCommand(cmd, &draft, body, a.suggestedRepo(target))
			}
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

// editCommand returns the wiki edit command that writes body, the content of a wiki create, with
// repo, the -R flag that names the repository, if any. Content that the command cannot carry, such
// as standard input or a long or multi-line body, goes to a draft, which the error then keeps.
func (c *wikiContent) editCommand(cmd *cobra.Command, draft *terminal.Draft, body, repo string) string {
	command := "gt wiki edit"
	// Gitea matches the trimmed title; one that starts with - goes after --, which ends the flags.
	title := argumentQuote(strings.TrimSpace(c.title))
	dashed := strings.HasPrefix(strings.TrimSpace(c.title), "-")
	if !dashed {
		command += " " + title
	}
	if draft.Path == "" && !(cmd.Flags().Changed("body-file") && c.file != "-") && !(cmd.Flags().Changed("body") && fitsWikiHint(c.body)) {
		*draft = keepDraft(body)
	}
	switch {
	case cmd.Flags().Changed("body-file") && c.file != "-":
		command += " -F " + argumentQuote(c.file)
	case cmd.Flags().Changed("body") && fitsWikiHint(c.body):
		command += " -b " + argumentQuote(c.body)
	case draft.Path != "":
		command += " -F " + argumentQuote(draft.Path)
	default:
		command += " --editor"
	}
	if c.message != "" {
		command += " -m " + argumentQuote(c.message)
	}
	command += repo
	if dashed {
		command += " -- " + title
	}
	return command
}

// fitsWikiHint reports whether body is short and plain enough to suggest as a -b flag.
func fitsWikiHint(body string) bool {
	return len(body) <= 80 && !strings.ContainsFunc(body, unicode.IsControl)
}

// argumentQuote quotes s as one argument of a suggested command line: in single quotes, or as
// $'...' with escapes when s holds control characters, such as a tab, which a terminal would not
// copy as they are.
func argumentQuote(s string) string {
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return shellQuote(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for _, r := range s {
		switch {
		case r == '\\' || r == '\'':
			b.WriteString(`\` + string(r))
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString("'")
	return b.String()
}

// wikiBody collects page content: in the editor with --editor, else in the editor or an inline
// field on a terminal. Without a terminal it fails, naming --body.
func (a *application) wikiBody(cmd *cobra.Command, editor bool, initial string) (terminal.Draft, error) {
	if editor {
		return a.editDraft(cmd, initial)
	}
	if !a.canPrompt() {
		return terminal.Draft{}, missingBody(cmd)
	}
	return a.bodyEditor(cmd, terminal.BodyTitle)(cmd.Context(), initial)
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
		Args: nonBlankArgs(cobra.ExactArgs(1), "page name"),
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			var draft terminal.Draft
			defer finishDraft(&draft, &runErr)
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			if !changed("title") && !changed("body") && !changed("body-file") && !content.editor {
				return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --title or --body (see --help)"))
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
				current, err := service.Find(cmd.Context(), args[0])
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
			item, renamed, err := service.Edit(cmd.Context(), args[0], services.WikiOptions{Title: content.title, Body: body, Message: content.message})
			if err != nil {
				return err
			}
			if renamed {
				a.done(cmd, "Renamed wiki page %q to %q in %s", strings.TrimSpace(args[0]), item.Title, target.Repo)
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
		Args:        nonBlankArgs(cobra.ExactArgs(1), "page name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Wiki{Client: target.client, Repository: target.Repo}
			page, err := service.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			subject := "wiki page " + strconv.Quote(page.Title) + " from " + target.Repo
			return a.deleteConfirmed(cmd, subject, func() error { return service.Delete(cmd.Context(), page.Title) })
		},
	}
	command.ValidArgsFunction = a.completeWikiPages()
	return command
}
