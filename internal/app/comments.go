// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// commentTitle is the prompt for a comment body.
const commentTitle = "Comment (markdown):"

// commentNouns are the words for the commands of an issue or pull request comment group.
type commentNouns struct {
	group, noun, arguments, example string
}

func commentWords(kind refKind) commentNouns {
	if kind == pullRef {
		return commentNouns{group: "pr", noun: "pull request", arguments: pullNumberArguments, example: "13"}
	}
	return commentNouns{group: "issue", noun: "issue", arguments: issueArguments, example: "12"}
}

// pullNumberArguments is the ARGUMENTS help of pr comment and pr attachment commands, which take pull
// request numbers like issue commands take issue numbers.
const pullNumberArguments = `A pull request can be supplied as argument in any of the following formats:
- by number, e.g. "13" or "#13"
- by URL, e.g. "https://gitea.com/OWNER/REPO/pulls/13"
- by repository and number, e.g. "OWNER/REPO#13"`

// requirePull fails unless issue n is a pull request, for pr commands that Gitea serves through
// issue endpoints and would otherwise apply to the issue with that number.
func requirePull(cmd *cobra.Command, t target, n int64) error {
	_, err := discussion(cmd, t, n, pullRef)
	return err
}

// discussion returns the issue or pull request n that a command of kind addresses: pr commands
// refuse issues, which Gitea would otherwise serve through the same endpoints.
func discussion(cmd *cobra.Command, t target, n int64, kind refKind) (services.Issue, error) {
	item, err := (services.Issues{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
	if err == nil && kind == pullRef && item.Kind != services.KindPull {
		err = fmt.Errorf("#%d in %s is an issue, not a pull request; use %s %d", n, t.Repo, issueCommandPath(cmd), n)
	}
	return item, err
}

// issueCommandPath names the issue command that matches a pr command, such as "gt issue comment".
func issueCommandPath(cmd *cobra.Command) string {
	return strings.Replace(cmd.CommandPath(), " pr ", " issue ", 1)
}

// commentName names a comment in messages: "comment 81 on alice/x#1".
func commentName(t target, comment services.Comment) string {
	return fmt.Sprintf("comment %d on %s#%d", comment.ID, t.Repo, comment.IssueNumber)
}

// commentFlags are the body flags of commands that write a comment.
type commentFlags struct {
	body, file string
	useEditor  bool
}

func (f *commentFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.body, "body", "b", "", "Comment `text`")
	cmd.Flags().StringVarP(&f.file, "body-file", "F", "", "Read the comment from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&f.useEditor, "editor", "e", false, "Write the comment in the configured editor")
}

// validate rejects conflicting or empty comment sources before any request.
func (f commentFlags) validate(cmd *cobra.Command) error {
	if err := validateBodySources(cmd); err != nil {
		return err
	}
	if cmd.Flags().Changed("body") && strings.TrimSpace(f.body) == "" {
		return clierr.Wrap(clierr.Usage, errors.New("comment body cannot be empty"))
	}
	return nil
}

// readComment returns the comment text: from --body or --body-file, else from the editor with
// --editor or when one is configured, else from a multi-line prompt. initial seeds the editor and
// the prompt.
func (a *application) readComment(cmd *cobra.Command, f commentFlags, initial string) (string, terminal.Draft, error) {
	var draft terminal.Draft
	var body string
	var err error
	switch {
	case cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file"):
		body, err = a.readBody(cmd, f.body, f.file)
	case f.useEditor:
		draft, err = a.editDraft(cmd, initial)
		body = draft.Body
	default:
		body, draft, err = a.promptComment(cmd, initial)
	}
	if err == nil && strings.TrimSpace(body) == "" {
		err = clierr.Wrap(clierr.Usage, errors.New("comment body cannot be empty"))
	}
	return body, draft, err
}

// promptComment asks for a comment in the configured editor, or in a multi-line field without one.
func (a *application) promptComment(cmd *cobra.Command, initial string) (string, terminal.Draft, error) {
	if !a.canPrompt() {
		return "", terminal.Draft{}, clierr.Wrap(clierr.Usage, errors.New("missing required input: supply --body, --body-file or --editor"))
	}
	if preferences, err := a.preferences(); err == nil && preferences.Editor != "" {
		draft, err := a.editDraft(cmd, initial)
		return draft.Body, draft, err
	}
	if asker, ok := a.deps.Prompter.(terminal.Asker); ok {
		body, err := asker.AskText(cmd.Context(), commentTitle, initial)
		return body, terminal.Draft{}, err
	}
	body, err := a.deps.Prompter.Input(cmd.Context(), commentTitle)
	return body, terminal.Draft{}, err
}

// commentOn creates a comment on n, reporting it like gh: its URL on stdout, or JSON.
func (a *application) commentOn(cmd *cobra.Command, t target, n int64, kind refKind, f commentFlags) (runErr error) {
	item, err := discussion(cmd, t, n, kind)
	if err != nil {
		return err
	}
	body, draft, err := a.readComment(cmd, f, "")
	defer finishDraft(&draft, &runErr)
	if err != nil {
		return err
	}
	comment, err := (services.Comments{Client: t.client, Repository: t.Repo}).Create(cmd.Context(), n, body)
	if err != nil {
		return err
	}
	if a.options.json != "" {
		return a.writeJSON(cmd, comment)
	}
	a.done(cmd, "Commented on %s", refLabel(t.Repo, n, item.Title))
	return a.printURL(cmd, comment.URL)
}

// lastComment returns the newest comment on n by the account gt acts as.
func lastComment(cmd *cobra.Command, t target, n int64) (services.Comment, error) {
	items, err := (services.Comments{Client: t.client, Repository: t.Repo}).List(cmd.Context(), n)
	if err != nil {
		return services.Comment{}, err
	}
	for i := len(items) - 1; i >= 0; i-- {
		if strings.EqualFold(items[i].Author, t.login()) {
			return items[i], nil
		}
	}
	return services.Comment{}, fmt.Errorf("%s has no comments on %s#%d", t.login(), t.Repo, n)
}

// comment returns comment id, which a command of kind may change: pr commands refuse comments on
// issues.
func (a *application) comment(cmd *cobra.Command, t target, id int64, kind refKind) (services.Comment, error) {
	current, err := (services.Comments{Client: t.client, Repository: t.Repo}).View(cmd.Context(), id)
	if err == nil && kind == pullRef && !current.Pull {
		err = fmt.Errorf("comment %d in %s is on an issue, not a pull request; use %s %d", id, t.Repo, issueCommandPath(cmd), id)
	}
	return current, err
}

// editComment replaces the text of comment, prefilling the prompt or editor with it.
func (a *application) editComment(cmd *cobra.Command, t target, comment services.Comment, f commentFlags) (runErr error) {
	body, draft, err := a.readComment(cmd, f, comment.Body)
	defer finishDraft(&draft, &runErr)
	if err != nil {
		return err
	}
	edited, err := (services.Comments{Client: t.client, Repository: t.Repo}).Edit(cmd.Context(), comment.ID, body)
	if err != nil {
		return err
	}
	edited.IssueNumber = comment.IssueNumber
	if a.options.json != "" {
		return a.writeJSON(cmd, edited)
	}
	a.done(cmd, "Edited %s", commentName(t, edited))
	return nil
}

// deleteComment deletes comment after a confirmation that defaults to no.
func (a *application) deleteComment(cmd *cobra.Command, t target, comment services.Comment) error {
	name := commentName(t, comment)
	if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + name + "?", NonTTY: "delete " + name}); err != nil {
		return err
	}
	if err := (services.Comments{Client: t.client, Repository: t.Repo}).Delete(cmd.Context(), comment.ID); err != nil {
		return err
	}
	a.done(cmd, "Deleted %s", name)
	return nil
}

// commentView shows one comment; now dates it on a terminal.
func commentView(t target, now time.Time) func(services.Comment) terminal.View {
	return func(comment services.Comment) terminal.View {
		return terminal.View{
			Title: fmt.Sprintf("Comment on %s#%d", t.Repo, comment.IssueNumber), ID: "#" + strconv.FormatInt(comment.ID, 10),
			Meta: comment.Author + " commented " + terminal.RelativeTime(now, terminal.ParseTime(comment.CreatedAt)),
			Fields: []terminal.Field{
				{Label: "ID", Value: strconv.FormatInt(comment.ID, 10), PipedOnly: true},
				{Label: "Author", Value: comment.Author, PipedOnly: true},
				{Label: "Created_at", Value: comment.CreatedAt, PipedOnly: true},
			},
			Body: comment.Body, Kind: "comment", URL: comment.URL,
		}
	}
}

// writeComments shows comments as gh shows a conversation: on a terminal a heading line per comment,
// "#79 alice • about 2 hours ago (edited)", above its indented body; otherwise key/value blocks
// each followed by its body and "--".
func (a *application) writeComments(cmd *cobra.Command, t target, n int64, items []services.Comment) error {
	if a.options.json != "" {
		return a.writeJSON(cmd, items)
	}
	d := a.display()
	if len(items) == 0 {
		if d.TTY {
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "no comments on %s#%d\n", t.Repo, n)
			return err
		}
		return nil
	}
	var text strings.Builder
	for i, comment := range items {
		if !d.TTY {
			text.WriteString(fmt.Sprintf("id:\t%d\nauthor:\t%s\ncreated_at:\t%s\nurl:\t%s\n--\n", comment.ID, terminal.SafeLine(comment.Author), comment.CreatedAt, terminal.SafeLine(comment.URL)))
			text.WriteString(strings.TrimRight(terminal.SafeText(comment.Body), "\n") + "\n--\n")
			continue
		}
		if i > 0 {
			text.WriteString("\n")
		}
		heading := terminal.Cell{Text: "#" + strconv.FormatInt(comment.ID, 10), Link: comment.URL, Bold: true}
		meta := terminal.SafeLine(comment.Author)
		if created := terminal.ParseTime(comment.CreatedAt); !created.IsZero() {
			meta += " • " + terminal.RelativeTime(d.Now, created)
		}
		if comment.UpdatedAt != "" && comment.UpdatedAt != comment.CreatedAt {
			meta += " (edited)"
		}
		text.WriteString(terminal.Table(nil, [][]terminal.Cell{{heading, {Text: meta, Muted: true}}}, d))
		body := strings.TrimSpace(comment.Body)
		if d.Markdown {
			body = strings.TrimSpace(terminal.Markdown(body, d))
		} else {
			body = terminal.SafeText(body)
		}
		for line := range strings.Lines(body) {
			text.WriteString("  " + line)
		}
		text.WriteString("\n")
	}
	return a.writeHuman(cmd, text.String())
}

func (a *application) commentCommand(kind refKind) *cobra.Command {
	words := commentWords(kind)
	var flags commentFlags
	var editLast, deleteLast bool
	group := &cobra.Command{Use: "comment {<number> | <url>}", Short: "Add a comment to " + article(words.noun), Long: fmt.Sprintf(`Add a comment to %[1]s, or edit or delete your last comment on it.

Without --body, --body-file or --editor, the configured editor opens, or a multi-line prompt
asks for the comment. The subcommands list, view, edit and delete comments by their ID.`, article(words.noun)), Example: fmt.Sprintf(`$ gt %[1]s comment %[2]s --body "Fixed in #14"
$ gt %[1]s comment %[2]s --body-file notes.md
# Change or remove your last comment
$ gt %[1]s comment %[2]s --edit-last --body "Fixed in #15"
$ gt %[1]s comment %[2]s --delete-last --yes
$ gt %[1]s comment list %[2]s`, words.group, words.example), Annotations: map[string]string{"gt.arguments": words.arguments, "gt.confirm": "true"}, Args: commentArgs(kind), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if localFlagsChanged(cmd) {
				return clierr.Wrap(clierr.Usage, &argumentError{problem: "missing " + words.noun + " number", cmd: cmd})
			}
			return cmd.Help()
		}
		if err := flags.validate(cmd); err != nil {
			return err
		}
		if editLast && deleteLast {
			return clierr.Wrap(clierr.Usage, errors.New("--edit-last and --delete-last cannot be combined"))
		}
		if deleteLast && (cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") || flags.useEditor) {
			return clierr.Wrap(clierr.Usage, errors.New("--delete-last takes no comment text"))
		}
		t, n, err := a.resolveRef(cmd, args[0], kind)
		if err != nil {
			return err
		}
		if !editLast && !deleteLast {
			return a.commentOn(cmd, t, n, kind, flags)
		}
		if _, err := discussion(cmd, t, n, kind); err != nil {
			return err
		}
		last, err := lastComment(cmd, t, n)
		if err != nil {
			return err
		}
		if deleteLast {
			return a.deleteComment(cmd, t, last)
		}
		return a.editComment(cmd, t, last, flags)
	}}
	group.SuggestionsMinimumDistance = 2
	flags.register(group)
	group.Flags().BoolVar(&editLast, "edit-last", false, "Edit your last comment instead of adding one")
	group.Flags().BoolVar(&deleteLast, "delete-last", false, "Delete your last comment")
	group.ValidArgsFunction = a.completeDiscussions(kind)

	var limit, page int
	list := &cobra.Command{Use: "list {<number> | <url>}", Short: "List the comments on " + article(words.noun), Example: fmt.Sprintf(`$ gt %[1]s comment list %[2]s
$ gt %[1]s comment list %[2]s --json id,author,body`, words.group, words.example), Annotations: map[string]string{"gt.arguments": words.arguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.resolveRef(cmd, args[0], kind)
		if err != nil {
			return err
		}
		if kind == pullRef {
			if err := requirePull(cmd, t, n); err != nil {
				return err
			}
		}
		items, err := (services.Comments{Client: t.client, Repository: t.Repo}).Page(cmd.Context(), n, limit, page)
		if err != nil {
			return err
		}
		return a.writeComments(cmd, t, n, items)
	}}
	pageFlags(list, &limit, &page)
	list.ValidArgsFunction = a.completeDiscussions(kind)

	view := &cobra.Command{Use: "view <comment-id>", Short: "View a comment", Example: fmt.Sprintf("$ gt %s comment view 81", words.group), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		comment, err := a.comment(cmd, t, id, kind)
		if err != nil {
			return err
		}
		return writeView(a, cmd, comment, commentView(t, a.deps.Now()))
	}}

	var createFlags commentFlags
	create := &cobra.Command{Use: "create {<number> | <url>}", Aliases: []string{"add"}, Short: "Add a comment to " + article(words.noun), Example: fmt.Sprintf(`$ gt %[1]s comment create %[2]s --body "Looks good"
$ gt %[1]s comment add %[2]s --editor`, words.group, words.example), Annotations: map[string]string{"gt.arguments": words.arguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := createFlags.validate(cmd); err != nil {
			return err
		}
		t, n, err := a.resolveRef(cmd, args[0], kind)
		if err != nil {
			return err
		}
		return a.commentOn(cmd, t, n, kind, createFlags)
	}}
	createFlags.register(create)
	create.ValidArgsFunction = a.completeDiscussions(kind)

	var editFlags commentFlags
	edit := &cobra.Command{Use: "edit <comment-id>", Short: "Edit a comment", Long: "Replace the text of a comment. Without --body or --body-file, the editor or the prompt starts\nfrom the current text.", Example: fmt.Sprintf(`$ gt %[1]s comment edit 81 --body "Fixed in #15"
$ gt %[1]s comment edit 81`, words.group), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := editFlags.validate(cmd); err != nil {
			return err
		}
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		comment, err := a.comment(cmd, t, id, kind)
		if err != nil {
			return err
		}
		return a.editComment(cmd, t, comment, editFlags)
	}}
	editFlags.register(edit)

	remove := &cobra.Command{Use: "delete <comment-id>", Short: "Delete a comment", Example: fmt.Sprintf("$ gt %s comment delete 81 --yes", words.group), Annotations: map[string]string{"gt.text": "comment deletion does not return a resource", "gt.confirm": "true"}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		comment, err := a.comment(cmd, t, id, kind)
		if err != nil {
			return err
		}
		return a.deleteComment(cmd, t, comment)
	}}
	group.AddCommand(list, view, create, edit, remove)
	return group
}

// commentArgs accepts at most an issue or pull request, and reports a misspelled subcommand, such
// as "craete", as one.
func commentArgs(kind refKind) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if _, err := parseRef(args[0], kind); errors.Is(err, errNotRef) {
				if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown command %q for %q; did you mean %s?", args[0], cmd.CommandPath(), strings.Join(suggestions, " or ")))
				}
			}
		}
		return usageArgs(cobra.MaximumNArgs(1))(cmd, args)
	}
}

// article returns noun with its indefinite article: "an issue", "a pull request".
func article(noun string) string {
	if strings.ContainsRune("aeiou", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}

// completeDiscussions completes the open issues or pull requests that a command of kind takes.
func (a *application) completeDiscussions(kind refKind) cobra.CompletionFunc {
	if kind == pullRef {
		complete := a.completePulls("open")
		return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return complete(cmd, args, toComplete)
		}
	}
	return a.completeIssueArgs("open", false)
}
