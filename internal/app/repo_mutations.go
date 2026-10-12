// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func repositoryRecord(item services.Repository) terminal.Record {
	return terminal.Record{Heading: []terminal.Cell{{Text: item.FullName, Bold: true}}, Lines: []string{item.URL}}
}

func cloneURLForProtocol(cloneURL, sshURL, protocol string) (remote string, ssh bool, err error) {
	remote = cloneURL
	if protocol == "ssh" {
		remote = sshURL
	}
	if remote == "" {
		return "", false, errors.New("Gitea did not return a clone URL")
	}
	ssh = settings.RemoteIsSSH(remote)
	return remote, ssh, nil
}

// apiSSHHost trusts the SSH authority of the API's ssh_url when the instance has no ssh_host,
// so servers whose SSH host or port differ from the web host clone without extra configuration.
func apiSSHHost(host settings.Host, sshURL string) settings.Host {
	if host.SSHHost != "" || !settings.RemoteIsSSH(sshURL) {
		return host
	}
	if u, err := settings.ParseRemote(sshURL); err == nil {
		if _, err := settings.SSHEndpoint(u.Host); err == nil {
			host.SSHHost = u.Host
		}
	}
	return host
}

// mirrorSourceURL returns a mirror's credential-free network source URL, or "" when there is none to add.
func mirrorSourceURL(item services.Repository) string {
	if !item.Mirror || item.OriginalURL == "" {
		return ""
	}
	u, err := url.Parse(item.OriginalURL)
	if err != nil || u.Host == "" || strings.ContainsAny(item.OriginalURL, " \t\r\n\\") {
		return ""
	}
	switch u.Scheme {
	case "https", "http", "git":
		if u.User != nil {
			return ""
		}
	case "ssh":
		if _, password := u.User.Password(); password {
			return ""
		}
	default:
		return ""
	}
	return u.String()
}

func (a *application) addRepositoryMutations(group *cobra.Command) {
	group.AddCommand(a.repoCreateCommand(), a.repoMigrateCommand())
	var forkOrg string
	fork := &cobra.Command{Use: "fork [repository]", Short: "Fork the selected repository", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Repositories{Client: t.client}).Fork(cmd.Context(), t.Repo, forkOrg)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, repositoryRecord)
	}}
	fork.Flags().StringVar(&forkOrg, "org", "", "Fork under an organization")
	group.AddCommand(fork)
	var description, repoName, website, defaultBranch string
	var private, template, repoArchived, actionsEnabled bool
	edit := &cobra.Command{Use: "edit [repository]", Short: "Edit repository settings", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		options := services.RepositoryEdit{}
		if cmd.Flags().Changed("name") {
			if strings.TrimSpace(repoName) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("repository name cannot be empty"))
			}
			options.Name = &repoName
		}
		if cmd.Flags().Changed("website") {
			options.Website = &website
		}
		if cmd.Flags().Changed("default-branch") {
			if strings.TrimSpace(defaultBranch) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("default branch cannot be empty"))
			}
			options.DefaultBranch = &defaultBranch
		}
		if cmd.Flags().Changed("archived") {
			options.Archived = &repoArchived
		}

		if cmd.Flags().Changed("description") {
			options.Description = &description
		}
		if cmd.Flags().Changed("private") {
			options.Private = &private
		}
		if cmd.Flags().Changed("as-template") {
			options.Template = &template
		}
		if cmd.Flags().Changed("actions") {
			options.ActionsEnabled = &actionsEnabled
		}
		if options == (services.RepositoryEdit{}) {
			return clierr.Wrap(clierr.Usage, errors.New("supply an edit flag; see gt repo edit --help"))
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if repoArchived {
			if err := a.confirm(cmd, "Archive repository "+t.Repo); err != nil {
				return err
			}
		}
		item, err := (services.Repositories{Client: t.client}).Edit(cmd.Context(), t.Repo, options)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, repositoryRecord)
	}}
	edit.Flags().StringVar(&repoName, "name", "", "Rename the repository")
	edit.Flags().StringVar(&website, "website", "", "Repository website; empty clears")
	edit.Flags().StringVar(&defaultBranch, "default-branch", "", "Default branch name")
	edit.Flags().BoolVar(&repoArchived, "archived", false, "Archive repository; =false restores it")
	edit.Flags().StringVar(&description, "description", "", "Repository description")
	edit.Flags().BoolVar(&private, "private", false, "Make repository private (use --private=false to make public)")
	edit.Flags().BoolVar(&template, "as-template", false, "Make repository a template (use =false to disable)")
	edit.Flags().BoolVar(&actionsEnabled, "actions", false, "Enable Actions (use --actions=false to disable)")
	group.AddCommand(edit)
	for _, verb := range []string{"archive", "delete"} {
		command := &cobra.Command{Use: verb + " [repository]", Short: titleCase(verb) + " the selected repository", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			inferred := ""
			if t.RepoSource != "--repo" {
				source := strings.Replace(t.RepoSource, "remote ", "git remote ", 1)
				if a.options.yes {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("--yes requires an explicit repository, not one inferred from %s: gt repo %s %s --yes", source, verb, t.Repo))
				}
				inferred = " (from " + source + ")"
			}
			service := services.Repositories{Client: t.client}
			if verb == "delete" {
				if err := a.confirmTyped(cmd, "Delete "+t.Repo+inferred+"? This cannot be undone.", t.Repo); err != nil {
					return err
				}
				return service.Delete(cmd.Context(), t.Repo)
			}
			if err := a.confirm(cmd, "Archive "+t.Repo+inferred+"?"); err != nil {
				return err
			}
			archived := true
			item, err := service.Edit(cmd.Context(), t.Repo, services.RepositoryEdit{Archived: &archived})
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, repositoryRecord)
		}}
		if verb == "delete" {
			command.Annotations = textOutput("repository deletion does not return a resource")
		}
		group.AddCommand(command)
	}
	var depth int
	clone := &cobra.Command{Use: "clone <repository> [directory]", Short: "Clone a repository using the configured Git protocol", Args: usageArgs(cobra.RangeArgs(1, 2)), RunE: func(cmd *cobra.Command, args []string) error {
		if depth < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--depth must be nonnegative; zero fetches full history"))
		}
		if a.options.repo != "" {
			return clierr.Wrap(clierr.Usage, errors.New("clone repository argument cannot be combined with --repo"))
		}
		a.options.repo = args[0]
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
		if err != nil {
			return err
		}
		directory := ""
		if len(args) == 2 {
			directory = args[1]
		}
		if err := a.cloneRepository(cmd, t, item, directory, depth); err != nil {
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		return nil
	}}
	clone.Flags().IntVarP(&depth, "depth", "d", 0, "Number of commits to fetch (0: full history)")
	group.AddCommand(clone)
}

// cloneRepository clones item with the configured Git protocol and adds fork and mirror remotes.
// An empty directory lets Git choose it from the repository name.
func (a *application) cloneRepository(cmd *cobra.Command, t target, item services.Repository, directory string, depth int) error {
	protocol := t.gitProtocol
	host := t.host
	if protocol == "ssh" {
		host = apiSSHHost(host, item.SSHURL)
	}
	hosts := map[string]settings.Host{t.Host: host}
	remote, remoteIsSSH, err := cloneURLForProtocol(item.CloneURL, item.SSHURL, protocol)
	if err != nil {
		return err
	}
	cloneHost, cloneRepo, err := settings.RepositoryURL(remote, hosts)
	if err != nil || cloneHost != t.Host || !strings.EqualFold(cloneRepo, t.Repo) || (protocol == "ssh") != remoteIsSSH {
		return errors.New("Gitea clone URL does not match the selected instance and repository; check the server root URL and configured ssh_host")
	}
	gitArgs := []string{"clone"}
	if depth > 0 {
		gitArgs = append(gitArgs, "--depth", fmt.Sprint(depth))
	}
	gitArgs = append(gitArgs, "--", remote)
	if directory != "" {
		gitArgs = append(gitArgs, directory)
	} else if directory = filepath.Base(strings.TrimSuffix(item.Name, ".git")); directory == "" || directory == "." || directory == "/" {
		directory = item.Name
	}
	credentialArgs, err := a.gitTransportArguments(t, remoteIsSSH)
	if err != nil {
		return err
	}
	gitArgs = append(credentialArgs, gitArgs...)
	if _, err := a.git(cmd.Context(), gitArgs...); err != nil {
		return err
	}
	clone := directory
	if !filepath.IsAbs(clone) {
		clone = filepath.Join(a.options.cwd, clone)
	}
	if item.Fork && item.Parent != nil {
		upstream, upstreamSSH, err := cloneURLForProtocol(item.Parent.CloneURL, item.Parent.SSHURL, protocol)
		if err != nil {
			return errors.New("cloned the fork, but Gitea did not return a parent clone URL")
		}
		upstreamHost, upstreamRepo, err := settings.RepositoryURL(upstream, hosts)
		if err != nil || upstreamHost != t.Host || !strings.EqualFold(upstreamRepo, item.Parent.FullName) || (protocol == "ssh") != upstreamSSH {
			return errors.New("cloned the fork, but its parent clone URL does not match the selected instance")
		}
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", "upstream", upstream); err != nil {
			return fmt.Errorf("cloned the fork, but could not add the upstream remote: %w", err)
		}
		// The default branch pulls from upstream, but pushes stay on the fork.
		if branch := item.Parent.DefaultBranch; branch != "" {
			for _, setting := range [][2]string{{"remote", "upstream"}, {"merge", "refs/heads/" + branch}, {"pushRemote", "origin"}} {
				if _, err := a.gitIn(cmd.Context(), clone, "config", "branch."+branch+"."+setting[0], setting[1]); err != nil {
					return fmt.Errorf("cloned the fork and added upstream, but could not configure branch tracking: %w", err)
				}
			}
		}
	}
	// The remote is named source because repository discovery prefers a remote named upstream.
	if source := mirrorSourceURL(item); source != "" {
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", "source", source); err != nil {
			return fmt.Errorf("cloned the mirror, but could not add the source remote: %w", err)
		}
	}
	return nil
}
