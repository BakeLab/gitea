// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// repositorySwitches are the on/off settings of repo edit: each flag sets one field, and =false
// turns the setting off.
var repositorySwitches = []struct {
	flag, usage string
	field       func(*services.RepositoryEdit) **bool
}{
	{"private", "Make the repository private (=false makes it public)", func(e *services.RepositoryEdit) **bool { return &e.Private }},
	{"as-template", "Make the repository a template", func(e *services.RepositoryEdit) **bool { return &e.Template }},
	{"enable-issues", "Enable issues", func(e *services.RepositoryEdit) **bool { return &e.Issues }},
	{"enable-wiki", "Enable the wiki", func(e *services.RepositoryEdit) **bool { return &e.Wiki }},
	{"enable-pull-requests", "Enable pull requests", func(e *services.RepositoryEdit) **bool { return &e.PullRequests }},
	{"enable-projects", "Enable projects", func(e *services.RepositoryEdit) **bool { return &e.Projects }},
	{"actions", "Enable Actions", func(e *services.RepositoryEdit) **bool { return &e.ActionsEnabled }},
	{"allow-merge-commit", "Allow merging pull requests with a merge commit", func(e *services.RepositoryEdit) **bool { return &e.MergeCommits }},
	{"allow-squash", "Allow squash merging pull requests", func(e *services.RepositoryEdit) **bool { return &e.Squash }},
	{"allow-rebase", "Allow rebase merging pull requests", func(e *services.RepositoryEdit) **bool { return &e.Rebase }},
	{"allow-manual-merge", "Allow marking pull requests merged outside Gitea as merged", func(e *services.RepositoryEdit) **bool { return &e.ManualMerge }},
	{"autodetect-manual-merge", "Mark pull requests as merged when their commits reach the base branch outside Gitea", func(e *services.RepositoryEdit) **bool { return &e.DetectMerge }},
	{"delete-branch-on-merge", "Delete head branches after merging pull requests", func(e *services.RepositoryEdit) **bool { return &e.DeleteBranch }},
}

// validTopic is the form Gitea accepts for a lowercased topic name.
var validTopic = regexp.MustCompile(`^[a-z0-9][-.a-z0-9]{0,34}$`)

// maxTopics is the number of topics Gitea allows per repository.
const maxTopics = 25

// finalTopics returns current without the removed topics and with the added ones, lowercased like Gitea does.
func finalTopics(current, add, remove []string) []string {
	removed := make(map[string]bool, len(remove))
	for _, topic := range remove {
		removed[strings.ToLower(topic)] = true
	}
	var topics []string
	for _, topic := range slices.Concat(current, add) {
		topic = strings.ToLower(topic)
		if !removed[topic] && !slices.Contains(topics, topic) {
			topics = append(topics, topic)
		}
	}
	return topics
}

func (a *application) repoEditCommand() *cobra.Command {
	var website, defaultBranch, description, visibility, mergeStyle string
	var addTopics, removeTopics []string
	switches := make([]bool, len(repositorySwitches))
	command := &cobra.Command{
		Use:   "edit [<repository>]",
		Short: "Edit repository settings",
		Long: `Edit the settings of a repository. Only the flags you pass change; on/off settings such as
--enable-wiki take =false to turn them off.`,
		Example: `# Change the description and the website
$ gt repo edit --description "Command line tool" --website https://example.com

# Make a repository private and disable its wiki
$ gt repo edit alice/project --visibility private --enable-wiki=false

# Squash pull requests by default and tag the repository
$ gt repo edit --default-merge-style squash --add-topic cli --add-topic go

# Let pull requests merged outside Gitea be marked as merged
$ gt repo edit --allow-manual-merge`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		Args:        nonBlankArgs(cobra.MaximumNArgs(1), "repository"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			changed := cmd.Flags().Changed
			options := services.RepositoryEdit{}
			for i, toggle := range repositorySwitches {
				if changed(toggle.flag) {
					*toggle.field(&options) = &switches[i]
				}
			}
			for _, field := range []struct {
				flag  string
				value *string
				into  **string
			}{{"default-branch", &defaultBranch, &options.DefaultBranch}, {"default-merge-style", &mergeStyle, &options.MergeStyle}} {
				if changed(field.flag) {
					if strings.TrimSpace(*field.value) == "" {
						return clierr.Wrap(clierr.Usage, fmt.Errorf("--%s cannot be empty", field.flag))
					}
					*field.into = field.value
				}
			}
			if changed("website") {
				options.Website = &website
			}
			if changed("description") {
				if err := validateRepositoryDescription(description); err != nil {
					return err
				}
				options.Description = &description
			}
			if changed("visibility") {
				if changed("private") {
					return clierr.Wrap(clierr.Usage, errors.New("--visibility cannot be combined with --private"))
				}
				private := visibility == "private"
				options.Private = &private
			}
			if err := requireNames("topic", "", addTopics, removeTopics); err != nil {
				return err
			}
			if err := refuseAddedAndRemoved("topic", addTopics, removeTopics); err != nil {
				return err
			}
			for _, topic := range addTopics {
				if !validTopic.MatchString(strings.ToLower(topic)) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid topic %q: use up to 35 letters, digits, - or ., starting with a letter or digit", topic))
				}
			}
			if len(addTopics) > maxTopics {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("a repository has at most %d topics, got %d to add", maxTopics, len(addTopics)))
			}
			if options == (services.RepositoryEdit{}) && len(addTopics)+len(removeTopics) == 0 {
				return clierr.Wrap(clierr.Usage, errors.New("nothing to edit; pass a flag such as --description (see --help)"))
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Repositories{Client: t.client}
			if options.DefaultBranch != nil {
				if _, err := (services.Branches{Client: t.client}).View(cmd.Context(), t.Repo, defaultBranch); err != nil {
					// An empty repository has no branches, and Gitea accepts any default branch for it.
					if !services.IsNotFound(err) {
						return services.Classify(err, "branch", strconv.Quote(defaultBranch), t.Repo)
					}
					item, viewErr := service.View(cmd.Context(), t.Repo)
					if viewErr != nil {
						return viewErr
					}
					if !item.Empty {
						return services.Classify(err, "branch", strconv.Quote(defaultBranch), t.Repo)
					}
				}
			}
			// Topics are replaced in one request; compute and check them before anything changes.
			var topics []string
			setTopics := false
			if len(addTopics)+len(removeTopics) > 0 {
				current, err := service.Topics(cmd.Context(), t.Repo)
				if services.IsNotFound(err) {
					return services.Classify(err, "repository", t.Repo, "")
				}
				if err != nil {
					return fmt.Errorf("list topics: %w", err)
				}
				topics = finalTopics(current, addTopics, removeTopics)
				if len(topics) > maxTopics {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("a repository has at most %d topics, the edit would leave %d", maxTopics, len(topics)))
				}
				setTopics = !slices.Equal(slices.Sorted(slices.Values(topics)), slices.Sorted(slices.Values(finalTopics(current, nil, nil))))
			}
			repo := t.Repo
			settings := options != (services.RepositoryEdit{})
			if settings {
				item, err := service.Edit(cmd.Context(), t.Repo, options)
				if err != nil {
					return err
				}
				repo = item.FullName
			}
			if setTopics {
				if err := service.SetTopics(cmd.Context(), repo, topics); err != nil {
					if settings {
						return fmt.Errorf("repository settings were changed, but setting the topics failed: %w", err)
					}
					return fmt.Errorf("set topics: %w", err)
				}
			}
			var edited []string
			local := cmd.LocalFlags()
			cmd.Flags().Visit(func(flag *pflag.Flag) {
				if local.Lookup(flag.Name) != nil {
					edited = append(edited, flag.Name)
				}
			})
			a.done(cmd, "Edited repository %s (%s)", repo, strings.Join(edited, ", "))
			if a.options.json == "" {
				return nil
			}
			item, err := service.View(cmd.Context(), repo)
			if err != nil {
				return err
			}
			return a.writeJSON(cmd, item)
		},
	}
	flags := command.Flags()
	flags.StringVarP(&description, "description", "d", "", "Repository description")
	flags.StringVar(&website, "website", "", "Repository website (empty clears it)")
	flags.StringVar(&defaultBranch, "default-branch", "", "Default branch; it must exist")
	enumFlag(command, &visibility, "visibility", "", "", "Repository visibility", "public", "private")
	enumFlag(command, &mergeStyle, "default-merge-style", "", "", "Default merge style for pull requests", "merge", "rebase", "rebase-merge", "squash", "fast-forward-only")
	flags.StringSliceVar(&addTopics, "add-topic", nil, "Add topics (repeatable or comma-separated)")
	flags.StringSliceVar(&removeTopics, "remove-topic", nil, "Remove topics (repeatable or comma-separated)")
	for i, toggle := range repositorySwitches {
		flags.BoolVar(&switches[i], toggle.flag, false, toggle.usage)
	}
	_ = command.RegisterFlagCompletionFunc("default-branch", a.completeBranches())
	command.ValidArgsFunction = a.completeRepos()
	return command
}
