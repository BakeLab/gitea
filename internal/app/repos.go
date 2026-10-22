// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// repositoryArguments documents the [<repository>] argument of repository commands.
const repositoryArguments = `A repository can be supplied as NAME (one of yours), OWNER/NAME, HOST/OWNER/NAME or a URL.
Without an argument, the repository of the current Git clone is used.`

// repositoryArgument selects the repository from an optional positional owner/name or URL.
func (a *application) repositoryArgument(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if a.options.repo != "" {
		return clierr.Wrap(clierr.Usage, errors.New("supply the repository as an argument or --repo, not both"))
	}
	a.options.repo = args[0]
	return nil
}

// rejectRepoFlag fails when --repo is passed to a command that acts on no repository; suggest
// words the command the user probably meant for the owner that --repo names.
func (a *application) rejectRepoFlag(cmd *cobra.Command, suggest func(owner string) string) error {
	if a.options.repo == "" {
		return nil
	}
	text := "--repo is not used by " + strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	if ref, err := settings.ParseRepository(a.options.repo, nil); err == nil && ref.Owner != "" {
		text += "; did you mean: " + suggest(ref.Owner)
	}
	return clierr.Wrap(clierr.Usage, errors.New(text))
}

func (a *application) repoCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "repo",
		Short: "Work with Gitea repositories",
		Long: `Work with Gitea repositories: list and view them, create, fork, clone and migrate them, keep
local branches and forks in sync, and change their settings.`,
	}
	group.AddCommand(a.repoListCommand(), a.repoViewCommand(), a.repositorySyncCommand(), a.repoSetDefaultCommand())
	a.addRepositoryMutations(group)
	return jsonOutput(group, jsonFields[services.Repository]())
}

func (a *application) repoSetDefaultCommand() *cobra.Command {
	var view, unset bool
	command := &cobra.Command{
		Use:   "set-default [<repository>]",
		Short: "Set the default repository for this clone",
		Long: `Set the repository that gt commands use in the current Git clone instead of the one of its Git
remotes (saved as gt.repository in the clone's Git configuration).`,
		Example: `# Make gt commands in this clone use the fork's parent
$ gt repo set-default alice/project

# Show the saved repository
$ gt repo set-default --view

# Go back to the repository of the Git remotes
$ gt repo set-default --unset`,
		Annotations: map[string]string{"gt.arguments": `A repository can be supplied as NAME (one of yours), OWNER/NAME, HOST/OWNER/NAME or a URL.
Without an argument, a terminal asks for one of the repositories of the clone's Git remotes.`, "gt.text": "set-default configures Git"},
		Args: nonBlankArgs(cobra.MaximumNArgs(1), "repository"),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case view && unset:
				return clierr.Wrap(clierr.Usage, errors.New("--view and --unset cannot be combined"))
			case (view || unset) && len(args) > 0:
				return clierr.Wrap(clierr.Usage, errors.New("--view and --unset take no repository"))
			}
			ctx := cmd.Context()
			if inside, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree"); inside != "true" {
				return clierr.Wrap(clierr.Usage, errors.New("set-default must run inside a Git clone"))
			}
			saved, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "config", "--local", "--get", "gt.repository")
			switch {
			case view && saved == "":
				a.warn(cmd, "No default repository is set for this clone; set one with gt repo set-default OWNER/REPO")
				return nil
			case view:
				_, err := fmt.Fprintln(cmd.OutOrStdout(), terminal.SafeLine(saved))
				return err
			case unset && saved == "":
				a.warn(cmd, "No default repository is set for this clone")
				return nil
			case unset:
				if _, err := a.git(ctx, "config", "--local", "--unset", "gt.repository"); err != nil {
					return err
				}
				a.done(cmd, "Removed the default repository %s from this clone", saved)
				return nil
			}
			if len(args) == 0 && a.options.repo == "" {
				chosen, err := a.chooseRemoteRepository(ctx)
				if err != nil {
					return err
				}
				args = []string{chosen}
			}
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(ctx, true)
			if err != nil {
				return err
			}
			item, err := (services.Repositories{Client: t.client}).View(ctx, t.Repo)
			if err != nil {
				return err
			}
			if _, err := a.git(ctx, "config", "--local", "gt.repository", t.Host+"/"+item.FullName); err != nil {
				return err
			}
			a.done(cmd, "Set %s as the default repository for this clone", item.FullName)
			return nil
		},
	}
	command.Flags().BoolVarP(&view, "view", "v", false, "Print the saved default repository")
	command.Flags().BoolVarP(&unset, "unset", "u", false, "Remove the saved default repository")
	command.ValidArgsFunction = a.completeRepos()
	return command
}

// chooseRemoteRepository asks which repository of the clone's Git remotes set-default saves, on
// instances gt can act on.
func (a *application) chooseRemoteRepository(ctx context.Context) (string, error) {
	required := clierr.Wrap(clierr.Usage, errors.New("repo set-default requires a repository argument"))
	chooser, ok := a.chooser()
	if !ok {
		return "", required
	}
	snapshot, err := a.settings()
	if err != nil {
		return "", err
	}
	var choices []terminal.Choice
	for _, remote := range a.gitRemotes(ctx) {
		host, repo, err := settings.RemoteRepository(remote[1], snapshot.Hosts.Hosts)
		value := host + "/" + repo
		if err != nil || !a.canActOn(host, snapshot.Hosts.Hosts) || slices.ContainsFunc(choices, func(c terminal.Choice) bool { return c.Value == value }) {
			continue
		}
		choices = append(choices, terminal.Choice{Label: repo + " (" + remote[0] + ")", Value: value})
	}
	if len(choices) == 0 {
		return "", required
	}
	return chooser.Choose(ctx, "Default repository for this clone:", choices, "")
}

func (a *application) repoListCommand() *cobra.Command {
	var opts services.RepositoryListOptions
	list := &cobra.Command{
		Use:   "list [<owner>]",
		Short: "List repositories",
		Long: `List the repositories you can access, those of a user or organization, or the repositories you
starred or watch.`,
		Example: `# List the repositories you can access
$ gt repo list

# List the forks of an organization
$ gt repo list acme --type fork

# List your starred repositories as JSON
$ gt repo list --starred --json full_name,url`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.rejectRepoFlag(cmd, func(owner string) string { return "gt repo list " + owner }); err != nil {
				return err
			}
			if len(args) > 0 {
				opts.Owner = args[0]
			}
			scopes := 0
			for _, selected := range []bool{opts.Owner != "", opts.Starred, opts.Watched} {
				if selected {
					scopes++
				}
			}
			if scopes > 1 {
				return clierr.Wrap(clierr.Usage, errors.New("an owner, --starred and --watched select different collections; pass one"))
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			result, err := (services.Repositories{Client: target.client}).ListPage(cmd.Context(), opts)
			if err != nil {
				return err
			}
			collection := repositoryCollection(opts)
			spec := listSpec[services.Repository]{Columns: repositoryColumns, Row: repositoryRow, Context: collection, Empty: "no " + collection}
			return writeTable(a, cmd, result.Items, spec, servicePage(result, opts.Page))
		},
	}
	pageFlags(list, &opts.Limit, &opts.Page)
	enumFlag(list, &opts.Kind, "type", "", "", "Only list repositories of this kind", "fork", "source", "mirror")
	list.Flags().BoolVar(&opts.Starred, "starred", false, "List your starred repositories")
	list.Flags().BoolVar(&opts.Watched, "watched", false, "List the repositories you watch")
	list.ValidArgsFunction = a.completeOrgs()
	return list
}

// repositoryCollection names the repositories that repo list selects, as in "forks owned by acme".
func repositoryCollection(opts services.RepositoryListOptions) string {
	kinds := map[string]string{"fork": "forks", "mirror": "mirrors", "source": "source repositories"}
	noun, ok := kinds[opts.Kind]
	if !ok {
		noun = "repositories"
	}
	switch {
	case opts.Owner != "":
		return noun + " owned by " + opts.Owner
	case opts.Starred:
		return "starred " + noun
	case opts.Watched:
		return "watched " + noun
	}
	return noun + " you can access"
}

// repositoryColumns head the rows of repositoryRow in repo list and search repo.
var repositoryColumns = []terminal.Column{{Header: "Name"}, {Header: "Description", Flex: true}, {Header: "Info"}, {Header: "Updated"}, {Header: "URL", PipedOnly: true}}

func repositoryRow(item services.Repository) []terminal.Cell {
	return []terminal.Cell{{Text: item.FullName, Link: item.URL}, {Text: item.Description}, {Text: strings.Join(repositoryInfo(item), ", ")}, {Time: terminal.ParseTime(item.UpdatedAt)}, {Text: item.URL}}
}

// repositoryInfo describes the visibility and the kind of a repository: public or private, then
// fork, mirror, template, archived or empty.
func repositoryInfo(item services.Repository) []string {
	info := []string{"public"}
	if item.Private {
		info[0] = "private"
	}
	for _, kind := range []struct {
		is   bool
		name string
	}{{item.Fork, "fork"}, {item.Mirror, "mirror"}, {item.Template, "template"}, {item.Archived, "archived"}, {item.Empty, "empty"}} {
		if kind.is {
			info = append(info, kind.name)
		}
	}
	return info
}

func (a *application) repoViewCommand() *cobra.Command {
	var web bool
	var branch string
	view := &cobra.Command{
		Use:   "view [<repository>]",
		Short: "View a repository",
		Long: `View a repository: its description, state, counts and clone URLs, followed by its README, which
is rendered on a terminal.`,
		Example: `# View the repository of the current clone
$ gt repo view

# View the README of another branch
$ gt repo view alice/project --branch develop

# Open the repository in the browser
$ gt repo view alice/project --web`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Repositories{Client: target.client}
			item, err := service.View(cmd.Context(), target.Repo)
			if err != nil {
				return err
			}
			if web && branch == "" {
				return a.openBrowser(cmd, item.URL)
			}
			if web {
				if _, err := (services.Branches{Client: target.client}).View(cmd.Context(), target.Repo, branch); err != nil {
					return err
				}
				return a.openBrowser(cmd, branchURL(item, branch))
			}
			readme := ""
			if a.options.json == "" && !item.Empty {
				if _, readme, err = service.Readme(cmd.Context(), target.Repo, branch); err != nil {
					return err
				}
			}
			return writeView(a, cmd, item, func(item services.Repository) terminal.View { return a.repositoryView(item, readme) })
		},
	}
	view.Flags().BoolVarP(&web, "web", "w", false, "Open the repository, or with --branch that branch, in the browser")
	view.Flags().StringVarP(&branch, "branch", "b", "", "Show the README of this branch (default: the default branch)")
	_ = view.RegisterFlagCompletionFunc("branch", a.completeArgumentBranches())
	view.ValidArgsFunction = a.completeRepos()
	return view
}

func (a *application) repositoryView(item services.Repository, readme string) terminal.View {
	badges := []terminal.Cell{{Text: "public", Color: "green"}}
	if item.Private {
		badges[0] = terminal.Cell{Text: "private", Color: "yellow"}
	}
	for _, kind := range repositoryInfo(item)[1:] {
		badge := terminal.Cell{Text: kind, Muted: true}
		if kind == "archived" {
			badge = terminal.Cell{Text: "archived (read-only)", Color: "yellow"}
		}
		badges = append(badges, badge)
	}
	meta := []string{
		fmt.Sprintf("★ %d", item.Stars), terminal.Plural(item.Forks, "fork"), terminal.Plural(item.OpenIssues, "open issue"),
		terminal.Plural(item.OpenPulls, "open pull request"),
	}
	if updated := terminal.ParseTime(item.UpdatedAt); !updated.IsZero() {
		meta = append(meta, "updated "+terminal.RelativeTime(a.deps.Now(), updated))
	}
	view := terminal.View{
		Title: item.FullName, Badges: badges, Meta: strings.Join(meta, " · "), Body: readme, Kind: "repository", URL: item.URL,
		Fields: []terminal.Field{
			{Label: "Description", Value: item.Description},
			{Label: "Forked from", Value: parentName(item)},
			{Label: "Mirror of", Value: mirrorSourceURL(item)},
			{Label: "Default branch", Value: item.DefaultBranch},
			{Label: "Website", Value: item.Website},
			{Label: "Topics", Value: strings.Join(item.Topics, ", ")},
			{Label: "Clone", Value: item.CloneURL},
			{Label: "SSH", Value: item.SSHURL},
		},
	}
	if a.deps.Streams.OutTTY && strings.TrimSpace(readme) == "" {
		view.Body = "_This repository has no README._"
		if item.Empty {
			view.Body = "_This repository is empty._"
		}
	}
	return view
}

func parentName(item services.Repository) string {
	if item.Parent == nil {
		return ""
	}
	return item.Parent.FullName
}
