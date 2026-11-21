// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

func (a *application) commitCommand() *cobra.Command {
	group := &cobra.Command{Use: "commit", Short: "Work with the commits of a repository", Long: `List the commits of a repository, look at one, show or compare changes, and see the state that CI
reported for a commit. A commit is named by its SHA, or by a branch or tag, which stands for the commit it
points to. These commands read the repository on the server: they need no clone.`}
	group.AddCommand(a.commitListCommand(), a.commitViewCommand(), a.commitDiffCommand(), a.commitCompareCommand(), a.commitStatusCommand(), a.commitPullCommand())
	return jsonOutput[services.Commit](group)
}

// commitBody is what follows the first line of a commit message and the blank line after it.
func commitBody(message string) string {
	_, body, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(body)
}

// commitWho names the author of a commit as Gitea knows them, else by the name in the commit.
func commitWho(commit services.Commit) string {
	return cmp.Or(commit.AuthorLogin, commit.Author)
}

// commitPerson writes a name and email as Git does; a commit may have no email.
func commitPerson(name, email string) string {
	if email == "" {
		return name
	}
	return strings.TrimSpace(name + " <" + email + ">")
}

// commitListSpec is the table of commits that list and compare show.
func (a *application) commitListSpec(context, empty string) listSpec[services.Commit] {
	tty := a.deps.Streams.OutTTY
	return listSpec[services.Commit]{
		Columns: []terminal.Column{{Header: "Commit"}, {Header: "Message", Flex: true}, {Header: "Author"}, {Header: "Date"}},
		Row: func(commit services.Commit) []terminal.Cell {
			sha := commit.SHA
			if tty {
				sha = shortSHA(sha) // piped output keeps the full SHA, for scripts
			}
			return []terminal.Cell{{Text: sha, Muted: true, Link: commit.URL}, {Text: terminal.SafeLine(messageSubject(commit.Message))}, {Text: terminal.SafeLine(commitWho(commit))}, {Time: terminal.ParseTime(commit.CommittedAt)}}
		},
		Context: context, Empty: empty,
	}
}

func (a *application) commitListCommand() *cobra.Command {
	var limit, page int
	var path, not string
	cmd := &cobra.Command{
		Use: "list [<ref>]", Aliases: []string{"ls"}, Short: "List commits", Args: nonBlankArgs(cobra.MaximumNArgs(1), "ref"), ValidArgsFunction: firstArg(a.completeBranches()),
		Long: `List the commits reachable from a branch, a tag or a commit, newest first; by default from the
default branch. --path keeps the commits that changed a file or directory and --not leaves out those
that another ref has too.

The additions, deletions and files of --json cost the server extra work and are read only when the
output includes them, as are verified and verification_reason.`,
		Example: `$ gt commit list
$ gt commit list release/1.2 --limit 50
$ gt commit list --path internal/app/ --not v1.0.0
$ gt commit list --json sha,author_login,message --jq '.[] | "\(.sha[:7]) \(.message | split("\n")[0])"'`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			opts := services.CommitListOptions{
				Path: path, Not: not,
				Stat: a.jsonSelects("additions", "deletions"), Files: a.jsonSelects("files"), Verification: a.jsonSelects("verified", "verification_reason"),
			}
			if len(args) > 0 {
				opts.Ref = args[0]
			}
			commits, err := (services.Commits{Client: target.client}).List(cmd.Context(), target.Repo, opts, limit, page)
			if err != nil {
				return err
			}
			return writeTable(a, cmd, commits.Items, a.commitListSpec("commits in "+target.Repo, "no commits in "+target.Repo), servicePage(commits, page))
		},
	}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().StringVar(&path, "path", "", "Only commits that changed this file or directory")
	cmd.Flags().StringVar(&not, "not", "", "Leave out the commits that this `ref` has too")
	return cmd
}

func (a *application) commitViewCommand() *cobra.Command {
	var web bool
	cmd := &cobra.Command{
		Use: "view <ref>", Short: "View a commit", Args: nonBlankArgs(cobra.ExactArgs(1), "ref"), ValidArgsFunction: firstArg(a.completeBranches()),
		Long: `View a commit: its message, author, signature check and the files it changed. For the changes
themselves use gt commit diff.`,
		Example: `$ gt commit view 3f2a9c1
$ gt commit view v1.2.0
$ gt commit view main --json sha,author_login,files`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			commit, err := (services.Commits{Client: target.client}).View(cmd.Context(), target.Repo, args[0])
			if err != nil {
				return err
			}
			if web {
				return a.openBrowser(cmd, commit.URL)
			}
			d := a.display()
			return writeView(a, cmd, commit, func(commit services.Commit) terminal.View {
				badges := []terminal.Cell{{Text: shortSHA(commit.SHA)}}
				if commit.Verified {
					badges = append(badges, terminal.Cell{Text: "verified", Color: "green"})
				}
				fields := []terminal.Field{
					{Label: "Commit", Value: commit.SHA, PipedOnly: true},
					{Label: "Author", Value: commitPerson(commitWho(commit), commit.AuthorEmail)},
					timeField("Authored", commit.AuthoredAt, d),
				}
				if commit.Committer != commit.Author || commit.CommitterEmail != commit.AuthorEmail {
					fields = append(fields, terminal.Field{Label: "Committer", Value: commitPerson(cmp.Or(commit.CommitterLogin, commit.Committer), commit.CommitterEmail)}, timeField("Committed", commit.CommittedAt, d))
				}
				fields = append(fields, terminal.Field{Label: "Parents", Value: strings.Join(shortSHAs(commit.Parents), ", ")})
				if commit.VerificationReason != "" && !commit.Verified && commit.VerificationReason != "gpg.error.not_signed_commit" {
					fields = append(fields, terminal.Field{Label: "Signature", Value: commit.VerificationReason})
				}
				fields = append(fields, terminal.Field{Label: "Changes", Value: fmt.Sprintf("%s, +%d -%d", terminal.Plural(len(commit.Files), "file"), commit.Additions, commit.Deletions)}, terminal.Field{Label: "Files", Value: commitFileList(commit.Files)})
				return terminal.View{Title: messageSubject(commit.Message), ID: "(" + shortSHA(commit.SHA) + ")", Badges: badges, Fields: fields, Body: commitBody(commit.Message), Plain: true, Kind: "commit", URL: commit.URL}
			})
		},
	}
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the commit in the browser")
	return jsonOutput[services.Commit](cmd)
}

func shortSHAs(shas []string) []string {
	short := make([]string, len(shas))
	for i, sha := range shas {
		short[i] = shortSHA(sha)
	}
	return short
}

// commitFileList names the first files of a commit and counts the rest.
func commitFileList(files []string) string {
	const shown = 20
	names := make([]string, 0, min(len(files), shown)+1)
	for _, file := range files[:min(len(files), shown)] {
		names = append(names, terminal.SafeLine(file))
	}
	if len(files) > shown {
		names = append(names, fmt.Sprintf("and %d more", len(files)-shown))
	}
	return strings.Join(names, ", ")
}

func (a *application) commitDiffCommand() *cobra.Command {
	var opts diffOptions
	var patch, web bool
	cmd := &cobra.Command{
		Use: "diff <ref>", Short: "Show the changes of a commit", Args: nonBlankArgs(cobra.ExactArgs(1), "ref"), ValidArgsFunction: firstArg(a.completeBranches()),
		Long: `Show the changes of a commit as a unified diff, the same way gt pr diff does.

On a terminal the diff is colored and paged. Piped, it is written unchanged, so that
gt commit diff 3f2a9c1 | git apply works. --patch writes the patch that git am takes, with the message and
author.

--path and --exclude narrow the diff to files, as the file tree of the web interface does, and
--name-only, --name-status and --stat summarize it. Gitea's API has no options for what a diff contains,
such as ignoring whitespace, so these work on the diff after gt has read it.`,
		Example: `$ gt commit diff 3f2a9c1
$ gt commit diff v1.2.0 --stat
$ gt commit diff main --path 'internal/' --exclude '*_test.go'
$ gt commit diff 3f2a9c1 --patch > fix.patch`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments, "gt.text": "diff returns patch text"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.validate(); err != nil {
				return err
			}
			if patch && (opts.narrowed() || opts.summary()) {
				return clierr.Wrap(clierr.Usage, errors.New("--patch cannot be combined with --path, --exclude, --name-only, --name-status or --stat"))
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			commits := services.Commits{Client: target.client}
			if web {
				commit, err := commits.View(cmd.Context(), target.Repo, args[0]) // the web route takes only a SHA
				if err != nil {
					return err
				}
				return a.openBrowser(cmd, commit.URL)
			}
			data, err := commits.Diff(cmd.Context(), target.Repo, args[0], patch)
			if err != nil {
				return err
			}
			return a.writeDiff(cmd, data, opts)
		},
	}
	opts.register(cmd)
	cmd.Flags().BoolVar(&patch, "patch", false, "Write the patch that git am takes, with the message and author")
	cmd.Flags().BoolVarP(&web, "web", "w", false, "Open the commit in the browser")
	return cmd
}

// compareRefs reads "<base>...<head>" or two arguments.
func compareRefs(args []string) (base, head string, err error) {
	if len(args) == 2 {
		base, head = args[0], args[1]
	} else {
		base, head, _ = strings.Cut(args[0], "...")
	}
	if strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" {
		return "", "", clierr.Wrap(clierr.Usage, errors.New("compare requires <base>...<head> or two refs"))
	}
	return base, head, nil
}

func (a *application) commitCompareCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "compare {<base>...<head> | <base> <head>}", Short: "List the commits that one ref has and another has not", Args: usageArgs(cobra.RangeArgs(1, 2)),
		Long: `List the commits that <head> has and <base> has not: what a pull request from <head> into <base>
would bring. Both are branches, tags or commits.`,
		Example: `$ gt commit compare main...feature/login
$ gt commit compare v1.1.0 v1.2.0 --json total_commits`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			base, head, err := compareRefs(args)
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			comparison, err := (services.Commits{Client: target.client}).Compare(cmd.Context(), target.Repo, base, head)
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, comparison)
			}
			context := fmt.Sprintf("commits that %s has and %s has not", terminal.SafeLine(head), terminal.SafeLine(base))
			return writeTable(a, cmd, comparison.Commits, a.commitListSpec(context, head+" has no commits that "+base+" has not"), listPage{Number: 1, Total: comparison.TotalCommits, Counted: true})
		},
	}
	return jsonOutput[services.Comparison](cmd)
}

func (a *application) commitStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "status <ref>", Short: "Show the state that CI reported for a commit", Args: nonBlankArgs(cobra.ExactArgs(1), "ref"), ValidArgsFunction: firstArg(a.completeBranches()),
		Long: `Show the latest status of each check that CI and other services reported for a commit, as
gt pr checks does for a pull request. The exit status is 1 when a check failed and 8 while one is
pending, so gt commit status main && deploy skips the deploy in both cases. A commit with no statuses
reports none. It checks once and does not wait.`,
		Example: `$ gt commit status main
$ gt commit status 3f2a9c1 --json name,state,url`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			items, err := (services.Commits{Client: target.client}).Status(cmd.Context(), target.Repo, args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				if err := a.writeJSON(cmd, items); err != nil {
					return err
				}
				return checksOutcome(items)
			}
			if len(items) == 0 {
				a.warn(cmd, "no statuses reported for %s", args[0])
				return nil
			}
			if err := a.writeHuman(cmd, a.checksText(items)); err != nil {
				return err
			}
			return checksOutcome(items)
		},
	}
	return jsonOutput[services.Check](cmd)
}

func (a *application) commitPullCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "pr <sha>", Short: "Find the pull request that merged a commit", Args: nonBlankArgs(cobra.ExactArgs(1), "sha"),
		Long: "Find the pull request that merged a commit, to learn why a change was made. It takes the SHA of a commit, not a branch.",
		Example: `$ gt commit pr 3f2a9c1
$ gt commit pr 3f2a9c1 --json number,title,url --jq .url`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			pull, err := (services.Commits{Client: target.client}).PullRequestOf(cmd.Context(), target.Repo, args[0])
			if err != nil {
				return err
			}
			return a.pullResult(cmd, target, pull)
		},
	}
	return jsonOutput[services.Pull](cmd)
}
