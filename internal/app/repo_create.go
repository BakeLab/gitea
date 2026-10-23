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
	"strconv"
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

type repoCreateFlags struct {
	opts                                     services.RepositoryOptions
	templateOpts                             services.RepositoryTemplateOptions
	org, template, team, localSource, remote string
	clone, public, push                      bool
}

func (a *application) repoCreateCommand() *cobra.Command {
	var f repoCreateFlags
	command := &cobra.Command{
		Use:     "create [<name>]",
		Aliases: []string{"new"},
		Short:   "Create a repository",
		Long: `Create a repository for your account, or for an organization as ORG/NAME or with --org. Site
administrators can also create one for another user as USER/NAME.

--auto-init adds a first commit with a README, .gitignore and license; --template copies another
repository. --source adds the new repository as a remote of a local Git repository, and --push
pushes its current branch there.

On a terminal, without a name or flags, it asks for the owner, name, description and visibility,
whether to add a README with a license and .gitignore templates, and whether to clone the new
repository.`,
		Example: `# Create a private repository with a README and an MIT license
$ gt repo create project --private --auto-init --license MIT

# Create a repository in an organization from a template
$ gt repo create acme/service --template acme/service-template

# Publish the local repository in the current directory
$ gt repo create project --source . --push`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runRepoCreate(cmd, args, &f)
		},
	}
	flags := command.Flags()
	flags.StringVar(&f.org, "org", "", "Create the repository in this organization")
	flags.StringVar(&f.team, "team", "", "Grant this organization team access")
	flags.BoolVar(&f.clone, "clone", false, "Clone the new repository into the current directory")
	flags.StringVarP(&f.opts.Description, "description", "d", "", "Repository description")
	flags.BoolVar(&f.opts.Private, "private", false, "Make the repository private")
	flags.BoolVar(&f.public, "public", false, "Make the repository public (the default)")
	flags.BoolVar(&f.opts.AutoInit, "auto-init", false, "Add a first commit with a README")
	flags.StringVar(&f.opts.DefaultBranch, "default-branch", "", "Name of the default branch")
	flags.StringVar(&f.opts.Labels, "label-template", "", "Add the labels of this label set")
	flags.StringVarP(&f.opts.Gitignores, "gitignores", "g", "", "Comma-separated .gitignore templates (requires --auto-init)")
	flags.StringVarP(&f.opts.License, "license", "l", "", "License template (requires --auto-init)")
	flags.StringVar(&f.opts.Readme, "readme", "", "README template (requires --auto-init)")
	flags.BoolVar(&f.opts.Template, "as-template", false, "Make the new repository a template")
	enumFlag(command, &f.opts.TrustModel, "trust-model", "", "", "Signature trust model", "default", "collaborator", "committer", "collaboratorcommitter")
	enumFlag(command, &f.opts.ObjectFormat, "object-format", "", "", "Git object format", "sha1", "sha256")
	flags.StringVar(&f.template, "template", "", "Copy this template repository (OWNER/REPO or URL)")
	flags.BoolVar(&f.templateOpts.Content, "copy-content", true, "Copy the template's Git content")
	flags.BoolVar(&f.templateOpts.GitHooks, "copy-git-hooks", true, "Copy the template's Git hooks")
	flags.BoolVar(&f.templateOpts.Avatar, "copy-avatar", true, "Copy the template's avatar")
	flags.BoolVar(&f.templateOpts.Labels, "copy-labels", true, "Copy the template's labels")
	flags.BoolVar(&f.templateOpts.Topics, "copy-topics", true, "Copy the template's topics")
	flags.BoolVar(&f.templateOpts.Webhooks, "copy-webhooks", false, "Copy the template's webhooks")
	flags.StringVar(&f.localSource, "source", "", "Publish the local Git repository in this directory")
	_ = command.MarkFlagDirname("source")
	flags.StringVar(&f.remote, "remote-name", "origin", "Name of the new repository's remote in --source")
	flags.BoolVar(&f.push, "push", false, "Push the current branch of --source")
	_ = command.RegisterFlagCompletionFunc("org", a.completeOrgs())
	_ = command.RegisterFlagCompletionFunc("template", a.completeRepos())
	_ = command.RegisterFlagCompletionFunc("license", a.completeTemplates(services.Repositories.Licenses))
	_ = command.RegisterFlagCompletionFunc("gitignores", commaSeparated(a.completeTemplates(services.Repositories.Gitignores)))
	_ = command.RegisterFlagCompletionFunc("label-template", a.completeTemplates(services.Repositories.LabelTemplates))
	return command
}

func (a *application) runRepoCreate(cmd *cobra.Command, args []string, f *repoCreateFlags) error {
	if len(args) > 0 {
		f.opts.Name = args[0]
	}
	if err := a.rejectRepoFlag(cmd, func(owner string) string { return "gt repo create " + owner + "/" + cmp.Or(f.opts.Name, "NAME") }); err != nil {
		return err
	}
	if owner, name, ok := strings.Cut(f.opts.Name, "/"); ok {
		if cmd.Flags().Changed("org") {
			return clierr.Wrap(clierr.Usage, errors.New("an OWNER/NAME argument cannot be combined with --org"))
		}
		f.org, f.opts.Name = owner, name
	}
	if err := validateCreateFlags(cmd, f.opts, f.org, f.team, f.template, f.localSource, f.clone); err != nil {
		return err
	}
	if f.public {
		if cmd.Flags().Changed("private") {
			return clierr.Wrap(clierr.Usage, errors.New("--public cannot be combined with --private"))
		}
		f.opts.Private = false
	}
	if f.localSource != "" {
		if !filepath.IsAbs(f.localSource) {
			f.localSource = filepath.Join(a.options.cwd, f.localSource)
		}
		if err := a.checkPublishSource(cmd.Context(), f.localSource, f.remote, f.push); err != nil {
			return err
		}
	}
	form := f.opts.Name == "" && !localFlagsChanged(cmd) && a.canPrompt() && a.deps.RepoForm != nil
	prompted := form
	if !form {
		var err error
		if prompted, err = a.requireInput(cmd, &f.opts.Name, "name", "Repository name", validateRepositoryName); err != nil {
			return err
		}
		if err := validateRepositoryName(f.opts.Name); err != nil {
			return err
		}
	}
	selected := &application{deps: a.deps, options: a.options}
	selected.options.repo = f.template
	target, err := selected.resolve(cmd.Context(), false)
	if err != nil {
		return err
	}
	service := services.Repositories{Client: target.client}
	if form {
		if err := a.askRepoSettings(cmd, target, service, f); err != nil {
			return err
		}
	} else if err := validateTemplates(cmd.Context(), service, &f.opts); err != nil {
		return err
	}
	if !cmd.Flags().Changed("org") && strings.EqualFold(f.org, target.User) {
		if f.team != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--team requires a repository owned by an organization"))
		}
		f.org = ""
	}
	if prompted {
		visibility := "public"
		if f.opts.Private {
			visibility = "private"
		}
		owner := cmp.Or(f.org, target.User)
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Create %s repository %s/%s on %s?", visibility, owner, f.opts.Name, target.Host), DefaultYes: true}); err != nil {
			return err
		}
	}
	var teamID int64
	if f.team != "" {
		teams, err := (services.Teams{Client: target.client}).ByName(cmd.Context(), f.org)
		if err != nil {
			return err
		}
		match, ok := teams[strings.ToLower(f.team)]
		if !ok {
			return services.NotFound("team", strconv.Quote(f.team), "organization "+f.org)
		}
		// Checked before creating, so that the repository is not left without the team.
		allowed, err := (services.Organizations{Client: target.client}).CanGrantTeamAccess(cmd.Context(), f.org, match.ID)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("--team requires an owner of %s, a site administrator, or a member of team %s when %s lets repository administrators change team access", f.org, match.Name, f.org)
		}
		teamID = match.ID
	}
	var item services.Repository
	if f.template != "" {
		f.templateOpts.Name, f.templateOpts.Owner, f.templateOpts.Description = f.opts.Name, f.org, f.opts.Description
		f.templateOpts.Private, f.templateOpts.DefaultBranch = f.opts.Private, f.opts.DefaultBranch
		item, err = service.Generate(cmd.Context(), target.Repo, f.templateOpts)
	} else {
		item, err = service.Create(cmd.Context(), f.org, f.opts)
	}
	if err != nil {
		return err
	}
	a.done(cmd, "Created repository %s", item.FullName)
	if teamID != 0 {
		if err := (services.Teams{Client: target.client}).SetRepository(cmd.Context(), teamID, item.FullName, true); err != nil {
			return fmt.Errorf("created %s, but could not grant team %q access: %w", item.FullName, f.team, err)
		}
		a.done(cmd, "Granted team %s access to %s", f.team, item.FullName)
	}
	target.Repo = item.FullName
	switch {
	case f.clone:
		// Record the new repository first so a failed clone does not hide that it exists.
		if err := a.created(cmd, item, item.URL); err != nil {
			return err
		}
		if _, err := a.cloneRepository(cmd, target, item, cloneOptions{upstream: "upstream"}); err != nil {
			return fmt.Errorf("created %s, but could not clone it: %w", item.FullName, err)
		}
		return nil
	case f.localSource != "":
		err = a.publishRepository(cmd, target, item, f.localSource, f.remote, f.push)
	}
	if err != nil {
		return err
	}
	return a.created(cmd, item, item.URL)
}

// askRepoSettings fills f from the interactive repository create form.
func (a *application) askRepoSettings(cmd *cobra.Command, t target, service services.Repositories, f *repoCreateFlags) error {
	loaded, err := service.CreateChoices(cmd.Context())
	if err != nil {
		return err
	}
	choices := terminal.RepoFormChoices{
		Owners: append([]string{t.User}, loaded.Organizations...), Licenses: loaded.Licenses, Gitignores: loaded.Gitignores,
		ValidateName: validateRepositoryName, ValidateDescription: validateRepositoryDescription,
	}
	values, err := a.deps.RepoForm.CreateRepo(cmd.Context(), choices)
	if err != nil {
		return err
	}
	f.org, f.opts.Name, f.opts.Description, f.opts.Private = values.Owner, values.Name, values.Description, values.Private
	f.opts.AutoInit, f.opts.License, f.opts.Gitignores = values.AutoInit, values.License, strings.Join(values.Gitignores, ",")
	f.clone = values.Clone
	return nil
}

func validateRepositoryDescription(description string) error {
	if utf8.RuneCountInString(description) > 2048 {
		return clierr.Wrap(clierr.Usage, errors.New("description cannot exceed 2048 characters"))
	}
	return nil
}

// validateCreateFlags rejects combinations of repo create flags that cannot work, before any request.
func validateCreateFlags(cmd *cobra.Command, opts services.RepositoryOptions, org, team, template, localSource string, clone bool) error {
	changed := func(names ...string) bool { return slices.ContainsFunc(names, cmd.Flags().Changed) }
	switch {
	case changed("team") && (org == "" || strings.TrimSpace(team) == ""):
		return clierr.Wrap(clierr.Usage, errors.New("--team requires --org and a team name"))
	case changed("template") && template == "":
		return clierr.Wrap(clierr.Usage, errors.New("--template requires owner/repo or a repository URL"))
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
	if err := validateRepositoryDescription(opts.Description); err != nil {
		return err
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

// checkPublishSource fails unless dir is a Git working tree without a remote named remote and,
// for push, with a commit on a checked-out branch to push.
func (a *application) checkPublishSource(ctx context.Context, dir, remote string, push bool) error {
	if inside, _ := a.gitProbe().Output(ctx, dir, false, "rev-parse", "--is-inside-work-tree"); inside != "true" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--source %s is not a Git repository; run git init first", dir))
	}
	if _, err := a.gitProbe().Output(ctx, dir, false, "remote", "get-url", remote); err == nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--source %s already has a remote %s; pass --remote-name", dir, remote))
	}
	if !push {
		return nil
	}
	if _, err := a.gitProbe().Output(ctx, dir, false, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--push requires a commit in --source %s; commit first", dir))
	}
	if _, err := a.gitProbe().Output(ctx, dir, false, "symbolic-ref", "--quiet", "HEAD"); err != nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--push requires a checked-out branch in --source %s", dir))
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
