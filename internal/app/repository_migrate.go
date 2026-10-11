// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"net/url"
	"path"
	"time"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"github.com/spf13/cobra"
)

func validateMigrationURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "git") {
		return clierr.Wrap(clierr.Usage, errors.New("clone URL must be an HTTP(S) or git URL without embedded credentials; server filesystem paths are not supported"))
	}
	return nil
}

func (a *application) repoMigrateCommand() *cobra.Command {
	var opts services.RepositoryMigrationOptions
	var passwordFile, tokenFile, serverPath string
	command := &cobra.Command{Use: "migrate [clone-url]", Short: "Import a remote repository through Gitea", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			if cmd.Flags().Changed("clone-url") {
				return clierr.Wrap(clierr.Usage, errors.New("supply clone URL as an argument or --clone-url, not both"))
			}
			opts.CloneURL = args[0]
		}
		if cmd.Flags().Changed("server-path") {
			if cmd.Flags().Changed("clone-url") || len(args) > 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--server-path and clone URL inputs are mutually exclusive"))
			}
			if !path.IsAbs(serverPath) {
				return clierr.Wrap(clierr.Usage, errors.New("--server-path must be an absolute path on the Gitea server"))
			}
			opts.CloneURL = serverPath
		}
		prompted := false
		for _, field := range []struct {
			value *string
			flag  string
		}{{&opts.CloneURL, "clone-url"}, {&opts.Name, "name"}} {
			completed, err := a.require(cmd, field.value, field.flag)
			if err != nil {
				return err
			}
			prompted = prompted || completed
		}
		if err := validateRepositoryName(opts.Name); err != nil {
			return err
		}
		if !cmd.Flags().Changed("server-path") {
			if err := validateMigrationURL(opts.CloneURL); err != nil {
				return err
			}
		}
		if cmd.Flags().Changed("server-path") && (opts.Service != "git" || opts.AuthUser != "" || cmd.Flags().Changed("auth-password") || cmd.Flags().Changed("auth-password-file") || cmd.Flags().Changed("auth-token") || cmd.Flags().Changed("auth-token-file")) {
			return clierr.Wrap(clierr.Usage, errors.New("server path imports use --service git without remote credentials"))
		}
		switch opts.Service {
		case "git", "gitea", "gitlab", "gogs", "github":
		default:
			return clierr.Wrap(clierr.Usage, errors.New("--service must be git, gitea, gitlab, gogs, or github"))
		}
		if opts.Timeout <= 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--timeout must be positive"))
		}
		if len(opts.Description) > 2048 {
			return clierr.Wrap(clierr.Usage, errors.New("description cannot exceed 2048 bytes"))
		}
		if opts.LFSEndpoint != "" {
			if !opts.LFS {
				return clierr.Wrap(clierr.Usage, errors.New("--lfs-endpoint requires --lfs"))
			}
			if err := validateMigrationURL(opts.LFSEndpoint); err != nil {
				return err
			}
		}
		if opts.MirrorInterval != "" {
			interval, err := time.ParseDuration(opts.MirrorInterval)
			if err != nil || interval <= 0 || !opts.Mirror {
				return clierr.Wrap(clierr.Usage, errors.New("--mirror-interval requires --mirror and a positive duration"))
			}
		}
		if opts.Mirror && (opts.Issues || opts.Milestones || opts.Labels || opts.PullRequests || opts.Releases) {
			return clierr.Wrap(clierr.Usage, errors.New("mirrors cannot copy issues, milestones, labels, pull requests, or releases"))
		}
		if opts.Service == "git" && (opts.Issues || opts.Milestones || opts.Labels || opts.PullRequests || opts.Releases) {
			return clierr.Wrap(clierr.Usage, errors.New("plain Git migration cannot copy forge metadata; select the source --service"))
		}
		if passwordFile == "-" && tokenFile == "-" {
			return clierr.Wrap(clierr.Usage, errors.New("stdin can supply only one source credential"))
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
		if opts.Service != "git" && opts.AuthToken == "" {
			return clierr.Wrap(clierr.Usage, errors.New("forge migration requires --auth-token or --auth-token-file"))
		}
		target, err := a.resolve(cmd.Context(), false)
		if err != nil {
			return err
		}
		if prompted {
			if err := a.confirm(cmd, "Migrate repository "+opts.Name+" to "+target.Host); err != nil {
				return err
			}
		}
		item, err := (services.Repositories{Client: target.client}).Migrate(cmd.Context(), opts)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, repositoryRecord)
	}}
	flags := command.Flags()
	flags.DurationVar(&opts.Timeout, "timeout", 10*time.Minute, "Maximum migration request duration; Gitea may continue an import after cancellation")
	flags.StringVar(&opts.Name, "name", "", "Destination repository name")
	flags.StringVar(&opts.Owner, "owner", "", "Destination owner (default: current account)")
	flags.StringVar(&serverPath, "server-path", "", "Absolute source Git repository path on the Gitea server (requires server permission)")
	flags.StringVar(&opts.CloneURL, "clone-url", "", "Remote repository URL")
	flags.StringVar(&opts.Service, "service", "git", "Source service: git, gitea, gitlab, gogs, github")
	flags.StringVar(&opts.Description, "description", "", "Repository description")
	flags.BoolVar(&opts.Mirror, "mirror", false, "Create a pull mirror")
	flags.BoolVar(&opts.Private, "private", false, "Make the imported repository private")
	flags.BoolVar(&opts.Wiki, "wiki", false, "Copy wiki")
	flags.BoolVar(&opts.Issues, "issues", false, "Copy issues")
	flags.BoolVar(&opts.Labels, "labels", false, "Copy labels")
	flags.BoolVar(&opts.PullRequests, "pull-requests", false, "Copy pull requests")
	flags.BoolVar(&opts.Releases, "releases", false, "Copy releases")
	flags.BoolVar(&opts.Milestones, "milestones", false, "Copy milestones")
	flags.StringVar(&opts.MirrorInterval, "mirror-interval", "", "Mirror interval, for example 8h")
	flags.BoolVar(&opts.LFS, "lfs", false, "Copy LFS objects")
	flags.StringVar(&opts.LFSEndpoint, "lfs-endpoint", "", "Source LFS endpoint")
	flags.StringVar(&opts.AuthUser, "auth-user", "", "Source authentication username")
	flags.StringVar(&opts.AuthPassword, "auth-password", "", "Source password (prefer file input)")
	flags.StringVar(&passwordFile, "auth-password-file", "", "Read source password from file or -")
	flags.StringVar(&opts.AuthToken, "auth-token", "", "Source API token (prefer file input)")
	flags.StringVar(&tokenFile, "auth-token-file", "", "Read source API token from file or -")
	return command
}
