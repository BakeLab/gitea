// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
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
	var org, source, team string
	var clone bool
	command := &cobra.Command{Use: "create [name]", Short: "Create a repository, optionally from a template", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if cmd.Flags().Changed("name") {
				return clierr.Wrap(clierr.Usage, errors.New("supply name as an argument or --name, not both"))
			}
			opts.Name = args[0]
		}
		if cmd.Flags().Changed("team") && (org == "" || strings.TrimSpace(team) == "") {
			return clierr.Wrap(clierr.Usage, errors.New("--team requires --org and a team name"))
		}
		if cmd.Flags().Changed("template") && source == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--template requires owner/repo or a repository URL"))
		}
		prompted, err := a.require(cmd, &opts.Name, "name")
		if err != nil {
			return err
		}
		if err := validateRepositoryName(opts.Name); err != nil {
			return err
		}
		if len(opts.Description) > 2048 {
			return clierr.Wrap(clierr.Usage, errors.New("description cannot exceed 2048 bytes"))
		}
		if opts.DefaultBranch != "" {
			if err := validateBranch(opts.DefaultBranch); err != nil {
				return err
			}
		}
		switch opts.TrustModel {
		case "", "committer", "collaborator", "collaborator+committer":
		default:
			return clierr.Wrap(clierr.Usage, errors.New("--trust-model must be committer, collaborator, or collaborator+committer"))
		}
		if opts.ObjectFormat != "" && opts.ObjectFormat != "sha1" && opts.ObjectFormat != "sha256" {
			return clierr.Wrap(clierr.Usage, errors.New("--object-format must be sha1 or sha256"))
		}
		if source == "" && (opts.Gitignores != "" || opts.License != "" || opts.Readme != "" || opts.DefaultBranch != "") && !opts.AutoInit {
			return clierr.Wrap(clierr.Usage, errors.New("initial content and default branch options require --auto-init"))
		}
		selected := *a
		if source != "" {
			if a.options.repo != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--template and --repo cannot both select a source repository"))
			}
			for _, flag := range []string{"auto-init", "label-template", "gitignores", "license", "readme", "as-template", "trust-model", "object-format"} {
				if cmd.Flags().Changed(flag) {
					return clierr.Wrap(clierr.Usage, errors.New("repository initialization options cannot be combined with --template"))
				}
			}
			selected.options.repo = source
		} else {
			for _, flag := range []string{"copy-content", "copy-git-hooks", "copy-avatar", "copy-labels", "copy-topics", "copy-webhooks"} {
				if cmd.Flags().Changed(flag) {
					return clierr.Wrap(clierr.Usage, errors.New("copy options require --template owner/repo"))
				}
			}
		}
		target, err := selected.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		if prompted {
			if err := a.confirm(cmd, "Create repository "+opts.Name+" on "+target.Host); err != nil {
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
		service := services.Repositories{Client: target.client}
		var item services.Repository
		if source != "" {
			templateOpts.Name = opts.Name
			templateOpts.Owner = org
			templateOpts.Description = opts.Description
			templateOpts.Private = opts.Private
			templateOpts.DefaultBranch = opts.DefaultBranch
			item, err = service.Generate(cmd.Context(), target.Repo, templateOpts)
		} else {
			item, err = service.Create(cmd.Context(), org, opts)
		}
		if err != nil {
			return err
		}
		if teamID != 0 {
			if err := (services.Teams{Client: target.client}).SetRepository(cmd.Context(), teamID, item.FullName, true); err != nil {
				return fmt.Errorf("created %s, but could not grant team %q access: %w", item.FullName, team, err)
			}
		}
		if err := writeRecord(a, cmd, item, repositoryRecord); err != nil || !clone {
			return err
		}
		target.Repo = item.FullName
		return a.cloneRepository(cmd, target, item, "", 0)
	}}
	flags := command.Flags()
	flags.StringVar(&opts.Name, "name", "", "Repository name")
	flags.StringVar(&org, "org", "", "Create under this organization")
	flags.StringVar(&team, "team", "", "Grant this organization team access (requires --org)")
	flags.BoolVar(&clone, "clone", false, "Clone the new repository into the current directory")
	flags.StringVar(&opts.Description, "description", "", "Repository description")
	flags.BoolVar(&opts.Private, "private", false, "Create a private repository")
	flags.BoolVar(&opts.AutoInit, "auto-init", false, "Initialize repository with a README")
	flags.StringVar(&opts.DefaultBranch, "default-branch", "", "Initial default branch")
	flags.StringVar(&opts.Labels, "label-template", "", "Named Gitea label set")
	flags.StringVar(&opts.Gitignores, "gitignores", "", "Comma-separated gitignore templates (requires --auto-init)")
	flags.StringVar(&opts.License, "license", "", "License template (requires --auto-init)")
	flags.StringVar(&opts.Readme, "readme", "", "README template (requires --auto-init)")
	flags.BoolVar(&opts.Template, "as-template", false, "Make the new repository a reusable template")
	flags.StringVar(&opts.TrustModel, "trust-model", "", "Signature trust model")
	flags.StringVar(&opts.ObjectFormat, "object-format", "", "Git object format: sha1 or sha256")
	flags.StringVar(&source, "template", "", "Source template owner/repo or repository URL")
	flags.BoolVar(&templateOpts.Content, "copy-content", true, "Copy template Git content")
	flags.BoolVar(&templateOpts.GitHooks, "copy-git-hooks", true, "Copy template Git hooks")
	flags.BoolVar(&templateOpts.Avatar, "copy-avatar", true, "Copy template avatar")
	flags.BoolVar(&templateOpts.Labels, "copy-labels", true, "Copy template labels")
	flags.BoolVar(&templateOpts.Topics, "copy-topics", true, "Copy template topics")
	flags.BoolVar(&templateOpts.Webhooks, "copy-webhooks", false, "Copy template webhooks")
	return command
}
