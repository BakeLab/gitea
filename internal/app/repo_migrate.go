// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func validateMigrationURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "git") {
		return clierr.Wrap(clierr.Usage, errors.New("clone URL must be an HTTP(S) or git URL without embedded credentials; server filesystem paths are not supported"))
	}
	return nil
}

// migrationName derives a repository name from the last segment of a clone URL or server path.
func migrationName(source string) string {
	if parsed, err := url.Parse(source); err == nil && parsed.Path != "" {
		source = parsed.Path
	}
	name := strings.TrimSuffix(path.Base(strings.TrimRight(source, "/")), ".git")
	if validateRepositoryName(name) != nil {
		return ""
	}
	return name
}

func (a *application) repoMigrateCommand() *cobra.Command {
	var opts services.RepositoryMigrationOptions
	var passwordFile, tokenFile, serverPath string
	command := &cobra.Command{
		Use:   "migrate [<clone-url>]",
		Short: "Import a repository from another server",
		Long: `Import a repository from a Git URL, or from another forge with --service (which can also copy
issues, pull requests, releases and more). A private source needs --auth-token or --auth-user and
--auth-password. --mirror creates a pull mirror instead. The name defaults to the last segment of
the URL.`,
		Example: `# Import a public Git repository
$ gt repo migrate https://github.com/go-gitea/gitea.git

# Mirror a repository into an organization every 8 hours
$ gt repo migrate https://github.com/go-gitea/gitea.git --owner acme --mirror --mirror-interval 8h

# Import a GitHub repository with its issues and pull requests
$ gt repo migrate https://github.com/acme/tool --service github --issues --pull-requests --auth-token-file token.txt`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.rejectRepoFlag(cmd, func(owner string) string { return "gt repo migrate URL --owner " + owner }); err != nil {
				return err
			}
			if len(args) > 0 {
				opts.CloneURL = args[0]
			}
			if cmd.Flags().Changed("server-path") {
				if len(args) > 0 {
					return clierr.Wrap(clierr.Usage, errors.New("--server-path and clone URL inputs are mutually exclusive"))
				}
				if !path.IsAbs(serverPath) {
					return clierr.Wrap(clierr.Usage, errors.New("--server-path must be an absolute path on the Gitea server"))
				}
				opts.CloneURL = serverPath
			}
			if err := validateMigrationFlags(cmd, opts, passwordFile, tokenFile); err != nil {
				return err
			}
			validURL := validateMigrationURL
			if cmd.Flags().Changed("server-path") {
				validURL = nil
			}
			prompted, err := a.requireInput(cmd, &opts.CloneURL, "clone-url", "Clone URL", validURL)
			if err != nil {
				return err
			}
			if validURL != nil {
				if err := validURL(opts.CloneURL); err != nil {
					return err
				}
			}
			opts.Name = cmp.Or(opts.Name, migrationName(opts.CloneURL))
			named, err := a.requireInput(cmd, &opts.Name, "name", "Repository name", validateRepositoryName)
			if err != nil {
				return err
			}
			if err := validateRepositoryName(opts.Name); err != nil {
				return err
			}
			for _, field := range []struct {
				value      *string
				file, flag string
			}{{&opts.AuthPassword, passwordFile, "auth-password"}, {&opts.AuthToken, tokenFile, "auth-token"}} {
				value, err := a.readSecret(cmd, *field.value, field.file, field.flag, field.flag+"-file")
				if err != nil {
					return err
				}
				*field.value = value
			}
			if opts.AuthToken != "" && (opts.AuthUser != "" || opts.AuthPassword != "") {
				return clierr.Wrap(clierr.Usage, errors.New("source token and username/password authentication are mutually exclusive"))
			}
			if opts.AuthPassword != "" && opts.AuthUser == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--auth-password requires --auth-user"))
			}
			target, err := a.resolve(cmd.Context(), false)
			if err != nil {
				return err
			}
			destination := cmp.Or(opts.Owner, target.User) + "/" + opts.Name
			if prompted || named {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Import %s into %s on %s?", opts.CloneURL, destination, target.Host), DefaultYes: true}); err != nil {
					return err
				}
			}
			item, err := (services.Repositories{Client: target.client}).Migrate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			detail := ""
			interval, _ := time.ParseDuration(opts.MirrorInterval)
			switch {
			case item.Mirror && opts.MirrorInterval != "" && interval == 0:
				detail = " (pull mirror, synced manually)"
			case item.Mirror && opts.MirrorInterval != "":
				detail = " (pull mirror, every " + opts.MirrorInterval + ")"
			case item.Mirror:
				detail = " (pull mirror)"
			}
			a.done(cmd, "Imported %s into %s%s", opts.CloneURL, item.FullName, detail)
			return a.created(cmd, item, item.URL)
		},
	}
	flags := command.Flags()
	flags.DurationVar(&opts.Timeout, "timeout", 10*time.Minute, "Maximum migration request duration; Gitea may continue an import after cancellation")
	flags.StringVar(&opts.Name, "name", "", "Destination repository name (default: the source's name)")
	flags.StringVar(&opts.Owner, "owner", "", "Destination user or organization (default: your account)")
	flags.StringVar(&serverPath, "server-path", "", "Absolute source Git repository path on the Gitea server (requires server permission)")
	_ = command.MarkFlagDirname("server-path")
	enumFlag(command, &opts.Service, "service", "", "git", "Source service", "git", "gitea", "gitlab", "gogs", "github")
	flags.StringVarP(&opts.Description, "description", "d", "", "Repository description")
	flags.BoolVar(&opts.Mirror, "mirror", false, "Create a pull mirror")
	flags.BoolVar(&opts.Private, "private", false, "Make the imported repository private")
	flags.BoolVar(&opts.Wiki, "wiki", false, "Copy wiki")
	flags.BoolVar(&opts.Issues, "issues", false, "Copy issues")
	flags.BoolVar(&opts.Labels, "labels", false, "Copy labels")
	flags.BoolVar(&opts.PullRequests, "pull-requests", false, "Copy pull requests")
	flags.BoolVar(&opts.Releases, "releases", false, "Copy releases")
	flags.BoolVar(&opts.Milestones, "milestones", false, "Copy milestones")
	flags.StringVar(&opts.MirrorInterval, "mirror-interval", "", "Mirror interval, for example 8h (0 turns off periodic sync)")
	flags.BoolVar(&opts.LFS, "lfs", false, "Copy LFS objects")
	flags.StringVar(&opts.LFSEndpoint, "lfs-endpoint", "", "Source LFS endpoint")
	flags.StringVar(&opts.AuthUser, "auth-user", "", "Source authentication username")
	flags.StringVar(&opts.AuthPassword, "auth-password", "", "Source password (prefer file input)")
	flags.StringVar(&passwordFile, "auth-password-file", "", "Read source password from file or -")
	flags.StringVar(&opts.AuthToken, "auth-token", "", "Source API token (prefer file input)")
	flags.StringVar(&tokenFile, "auth-token-file", "", "Read source API token from file or -")
	_ = command.RegisterFlagCompletionFunc("owner", a.completeOrgs())
	return command
}

// validateMigrationFlags rejects combinations of repo migrate flags that cannot work, before any
// prompt or request.
func validateMigrationFlags(cmd *cobra.Command, opts services.RepositoryMigrationOptions, passwordFile, tokenFile string) error {
	changed := cmd.Flags().Changed
	metadata := opts.Issues || opts.Milestones || opts.Labels || opts.PullRequests || opts.Releases
	switch {
	case changed("server-path") && (opts.Service != "git" || opts.AuthUser != "" || changed("auth-password") || changed("auth-password-file") || changed("auth-token") || changed("auth-token-file")):
		return clierr.Wrap(clierr.Usage, errors.New("server path imports use --service git without remote credentials"))
	case opts.Timeout <= 0:
		return clierr.Wrap(clierr.Usage, errors.New("--timeout must be positive"))
	case utf8.RuneCountInString(opts.Description) > 2048:
		return clierr.Wrap(clierr.Usage, errors.New("description cannot exceed 2048 characters"))
	case opts.LFSEndpoint != "" && !opts.LFS:
		return clierr.Wrap(clierr.Usage, errors.New("--lfs-endpoint requires --lfs"))
	case opts.Mirror && metadata:
		return clierr.Wrap(clierr.Usage, errors.New("mirrors cannot copy issues, milestones, labels, pull requests, or releases"))
	case opts.Service == "git" && metadata:
		return clierr.Wrap(clierr.Usage, errors.New("plain Git migration cannot copy forge metadata; select the source --service"))
	case passwordFile == "-" && tokenFile == "-":
		return clierr.Wrap(clierr.Usage, errors.New("stdin can supply only one source credential"))
	}
	if opts.LFSEndpoint != "" {
		if err := validateMigrationURL(opts.LFSEndpoint); err != nil {
			return err
		}
	}
	if opts.MirrorInterval != "" {
		interval, err := time.ParseDuration(opts.MirrorInterval)
		if err != nil || interval < 0 || !opts.Mirror {
			return clierr.Wrap(clierr.Usage, errors.New("--mirror-interval requires --mirror and a duration of 0 or more"))
		}
	}
	return nil
}
