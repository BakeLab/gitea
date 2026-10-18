// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func validateRepositoryName(name string) error {
	if name == "" || len(name) > 100 || name == "." || name == ".." || strings.ContainsAny(name, "/\\ \t\r\n") {
		return clierr.Wrap(clierr.Usage, errors.New("repository name must be nonempty, at most 100 bytes, and contain no path separators or whitespace"))
	}
	return nil
}

func (a *application) repoCreateCommand() *cobra.Command {
	var opts services.RepositoryOptions
	var templateOpts services.RepositoryTemplateOptions
	var org, template, team, localSource, remote string
	var clone, public, push bool
	command := &cobra.Command{
		Use:     "create [<name>]",
		Aliases: []string{"new"},
		Short:   "Create a repository",
		Long: `Create a repository for your account, or for an organization as ORG/NAME or with --org.

--auto-init adds a first commit with a README, .gitignore and license; --template copies another
repository. --source adds the new repository as a remote of a local Git repository, and --push
pushes its current branch there.`,
		Example: `# Create a private repository with a README and an MIT license
$ gt repo create project --private --auto-init --license MIT

# Create a repository in an organization from a template
$ gt repo create acme/service --template acme/service-template

# Publish the local repository in the current directory
$ gt repo create project --source . --push`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Name = args[0]
			}
			if err := a.rejectRepoFlag(cmd, func(owner string) string { return "gt repo create " + owner + "/" + cmp.Or(opts.Name, "NAME") }); err != nil {
				return err
			}
			if owner, name, ok := strings.Cut(opts.Name, "/"); ok {
				if cmd.Flags().Changed("org") {
					return clierr.Wrap(clierr.Usage, errors.New("supply the owner as OWNER/NAME or --org, not both"))
				}
				org, opts.Name = owner, name
			}
			if err := validateCreateFlags(cmd, opts, org, team, template, localSource, clone); err != nil {
				return err
			}
			if public {
				if cmd.Flags().Changed("private") {
					return clierr.Wrap(clierr.Usage, errors.New("--public and --private cannot be combined"))
				}
				opts.Private = false
			}
			if localSource != "" {
				if !filepath.IsAbs(localSource) {
					localSource = filepath.Join(a.options.cwd, localSource)
				}
				if err := a.checkPublishSource(cmd.Context(), localSource, remote); err != nil {
					return err
				}
			}
			prompted, err := a.requireInput(cmd, &opts.Name, "name", "Repository name", validateRepositoryName)
			if err != nil {
				return err
			}
			if err := validateRepositoryName(opts.Name); err != nil {
				return err
			}
			selected := &application{deps: a.deps, options: a.options}
			selected.options.repo = template
			target, err := selected.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("org") && strings.EqualFold(org, target.User) {
				if team != "" {
					return clierr.Wrap(clierr.Usage, errors.New("--team requires a repository owned by an organization"))
				}
				org = ""
			}
			service := services.Repositories{Client: target.client}
			if err := validateTemplates(cmd.Context(), service, &opts); err != nil {
				return err
			}
			if prompted {
				visibility := "public"
				if opts.Private {
					visibility = "private"
				}
				owner := cmp.Or(org, target.User)
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Create %s repository %s/%s on %s?", visibility, owner, opts.Name, target.Host), DefaultYes: true}); err != nil {
					return err
				}
			}
			var teamID int64
			if team != "" {
				teams, err := (services.Teams{Client: target.client}).ByName(cmd.Context(), org)
				if err != nil {
					return err
				}
				match, ok := teams[strings.ToLower(team)]
				if !ok {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("team %q not found in organization %s", team, org))
				}
				teamID = match.ID
			}
			var item services.Repository
			if template != "" {
				templateOpts.Name, templateOpts.Owner, templateOpts.Description = opts.Name, org, opts.Description
				templateOpts.Private, templateOpts.DefaultBranch = opts.Private, opts.DefaultBranch
				item, err = service.Generate(cmd.Context(), target.Repo, templateOpts)
			} else {
				item, err = service.Create(cmd.Context(), org, opts)
			}
			if err != nil {
				return err
			}
			a.done(cmd, "Created repository %s", item.FullName)
			if teamID != 0 {
				if err := (services.Teams{Client: target.client}).SetRepository(cmd.Context(), teamID, item.FullName, true); err != nil {
					return fmt.Errorf("created %s, but could not grant team %q access: %w", item.FullName, team, err)
				}
				a.done(cmd, "Granted team %s access to %s", team, item.FullName)
			}
			target.Repo = item.FullName
			switch {
			case clone:
				// Record the new repository first so a failed clone does not hide that it exists.
				if err := a.created(cmd, item, item.URL); err != nil {
					return err
				}
				if _, err := a.cloneRepository(cmd, target, item, cloneOptions{upstream: "upstream"}); err != nil {
					return fmt.Errorf("created %s, but could not clone it: %w", item.FullName, err)
				}
				return nil
			case localSource != "":
				err = a.publishRepository(cmd, target, item, localSource, remote, push)
			}
			if err != nil {
				return err
			}
			return a.created(cmd, item, item.URL)
		},
	}
	flags := command.Flags()
	flags.StringVar(&org, "org", "", "Create the repository in this organization")
	flags.StringVar(&team, "team", "", "Grant this organization team access")
	flags.BoolVar(&clone, "clone", false, "Clone the new repository into the current directory")
	flags.StringVarP(&opts.Description, "description", "d", "", "Repository description")
	flags.BoolVar(&opts.Private, "private", false, "Make the repository private")
	flags.BoolVar(&public, "public", false, "Make the repository public (the default)")
	flags.BoolVar(&opts.AutoInit, "auto-init", false, "Add a first commit with a README")
	flags.StringVar(&opts.DefaultBranch, "default-branch", "", "Name of the default branch")
	flags.StringVar(&opts.Labels, "label-template", "", "Add the labels of this label set")
	flags.StringVarP(&opts.Gitignores, "gitignores", "g", "", "Comma-separated .gitignore templates (requires --auto-init)")
	flags.StringVarP(&opts.License, "license", "l", "", "License template (requires --auto-init)")
	flags.StringVar(&opts.Readme, "readme", "", "README template (requires --auto-init)")
	flags.BoolVar(&opts.Template, "as-template", false, "Make the new repository a template")
	enumFlag(command, &opts.TrustModel, "trust-model", "", "", "Signature trust model", "default", "collaborator", "committer", "collaboratorcommitter")
	enumFlag(command, &opts.ObjectFormat, "object-format", "", "", "Git object format", "sha1", "sha256")
	flags.StringVar(&template, "template", "", "Copy this template repository (OWNER/REPO or URL)")
	flags.BoolVar(&templateOpts.Content, "copy-content", true, "Copy the template's Git content")
	flags.BoolVar(&templateOpts.GitHooks, "copy-git-hooks", true, "Copy the template's Git hooks")
	flags.BoolVar(&templateOpts.Avatar, "copy-avatar", true, "Copy the template's avatar")
	flags.BoolVar(&templateOpts.Labels, "copy-labels", true, "Copy the template's labels")
	flags.BoolVar(&templateOpts.Topics, "copy-topics", true, "Copy the template's topics")
	flags.BoolVar(&templateOpts.Webhooks, "copy-webhooks", false, "Copy the template's webhooks")
	flags.StringVar(&localSource, "source", "", "Publish the local Git repository in this directory")
	_ = command.MarkFlagDirname("source")
	flags.StringVar(&remote, "remote-name", "origin", "Name of the new repository's remote in --source")
	flags.BoolVar(&push, "push", false, "Push the current branch of --source")
	_ = command.RegisterFlagCompletionFunc("org", a.completeOrgs())
	_ = command.RegisterFlagCompletionFunc("template", a.completeRepos())
	_ = command.RegisterFlagCompletionFunc("license", a.completeTemplates(services.Repositories.Licenses))
	_ = command.RegisterFlagCompletionFunc("gitignores", commaSeparated(a.completeTemplates(services.Repositories.Gitignores)))
	_ = command.RegisterFlagCompletionFunc("label-template", a.completeTemplates(services.Repositories.LabelTemplates))
	return command
}

// validateCreateFlags rejects combinations of repo create flags that cannot work, before any request.
func validateCreateFlags(cmd *cobra.Command, opts services.RepositoryOptions, org, team, template, localSource string, clone bool) error {
	changed := func(names ...string) bool { return slices.ContainsFunc(names, cmd.Flags().Changed) }
	switch {
	case changed("team") && (org == "" || strings.TrimSpace(team) == ""):
		return clierr.Wrap(clierr.Usage, errors.New("--team requires --org and a team name"))
	case changed("template") && template == "":
		return clierr.Wrap(clierr.Usage, errors.New("--template requires owner/repo or a repository URL"))
	case utf8.RuneCountInString(opts.Description) > 2048:
		return clierr.Wrap(clierr.Usage, errors.New("description cannot exceed 2048 characters"))
	case template == "" && (opts.Gitignores != "" || opts.License != "" || opts.Readme != "" || opts.DefaultBranch != "") && !opts.AutoInit:
		return clierr.Wrap(clierr.Usage, errors.New("initial content and default branch options require --auto-init"))
	case template != "" && changed("auto-init", "label-template", "gitignores", "license", "readme", "as-template", "trust-model", "object-format"):
		return clierr.Wrap(clierr.Usage, errors.New("repository initialization options cannot be combined with --template"))
	case template == "" && changed("copy-content", "copy-git-hooks", "copy-avatar", "copy-labels", "copy-topics", "copy-webhooks"):
		return clierr.Wrap(clierr.Usage, errors.New("copy options require --template owner/repo"))
	case localSource != "" && (clone || template != "" || opts.AutoInit):
		return clierr.Wrap(clierr.Usage, errors.New("--source publishes a local repository; it cannot be combined with --clone, --template or --auto-init"))
	case localSource == "" && changed("push", "remote-name"):
		return clierr.Wrap(clierr.Usage, errors.New("--push and --remote-name require --source"))
	}
	if opts.DefaultBranch != "" {
		return validateBranch(opts.DefaultBranch)
	}
	return nil
}

// validateTemplates checks the license, .gitignore and label templates of opts against the
// server's lists, and spells them as the server does.
func validateTemplates(ctx context.Context, service services.Repositories, opts *services.RepositoryOptions) error {
	for _, field := range []struct {
		value      *string
		kind, flag string
		list       func(services.Repositories, context.Context) ([]string, error)
		multi      bool
	}{
		{&opts.License, "license", "license", services.Repositories.Licenses, false},
		{&opts.Gitignores, ".gitignore template", "gitignores", services.Repositories.Gitignores, true},
		{&opts.Labels, "label template", "label-template", services.Repositories.LabelTemplates, false},
	} {
		if *field.value == "" {
			continue
		}
		known, err := field.list(service, ctx)
		if err != nil {
			return err
		}
		names := []string{*field.value}
		if field.multi {
			names = strings.Split(*field.value, ",")
		}
		for i, name := range names {
			at := slices.IndexFunc(known, func(k string) bool { return strings.EqualFold(k, strings.TrimSpace(name)) })
			if at < 0 {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown %s %q; complete --%s with Tab to see the server's %ss", field.kind, strings.TrimSpace(name), field.flag, field.kind))
			}
			names[i] = known[at]
		}
		*field.value = strings.Join(names, ",")
	}
	return nil
}

// completeTemplates completes the names that list returns, such as the server's licenses.
func (a *application) completeTemplates(list func(services.Repositories, context.Context) ([]string, error)) cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		names, err := list(services.Repositories{Client: t.client}, ctx)
		return describe(names, func(name string) (string, string) { return name, "" }), err
	})
}

// checkPublishSource fails unless dir is a Git working tree without a remote named remote.
func (a *application) checkPublishSource(ctx context.Context, dir, remote string) error {
	if inside, _ := a.gitProbe().Output(ctx, dir, false, "rev-parse", "--is-inside-work-tree"); inside != "true" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--source %s is not a Git repository; run git init first", dir))
	}
	if _, err := a.gitProbe().Output(ctx, dir, false, "remote", "get-url", remote); err == nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--source %s already has a remote %s; pass --remote-name", dir, remote))
	}
	return nil
}

// publishRepository adds item as the remote of the local repository in dir and, with push,
// pushes the current branch to it.
func (a *application) publishRepository(cmd *cobra.Command, t target, item services.Repository, dir, remote string, push bool) error {
	url, ssh, err := cloneURLForProtocol(item.CloneURL, item.SSHURL, t.gitProtocol)
	if err != nil {
		return err
	}
	if _, err := a.gitIn(cmd.Context(), dir, "remote", "add", "--", remote, url); err != nil {
		return fmt.Errorf("created %s, but could not add it as Git remote %s: %w", item.FullName, remote, err)
	}
	a.done(cmd, "Added Git remote %s for %s", remote, item.FullName)
	if !push {
		return nil
	}
	credentials, err := a.gitTransportArguments(t, ssh)
	if err != nil {
		return err
	}
	if _, err := a.gitIn(cmd.Context(), dir, append(credentials, "push", "--quiet", "--set-upstream", remote, "HEAD")...); err != nil {
		return fmt.Errorf("created %s, but could not push to it: %w", item.FullName, err)
	}
	a.done(cmd, "Pushed the current branch to %s", item.FullName)
	return nil
}
