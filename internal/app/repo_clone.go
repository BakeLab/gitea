// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
)

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

func (a *application) repoCloneCommand() *cobra.Command {
	var opts cloneOptions
	var noUpstream bool
	command := &cobra.Command{
		Use:   "clone <repository> [<directory>] [-- <gitflags>...]",
		Short: "Clone a repository locally",
		Long: `Clone a repository with the configured Git protocol and gt's credentials. Git flags can
follow --. A fork's parent is added as the "upstream" remote, and gt commands in the clone then
target the parent. A pull mirror's source is added as the "source" remote; gt commands keep
targeting the mirror.`,
		Example: `# Clone a repository into ./project
$ gt repo clone alice/project

# Clone into another directory with the last commit only
$ gt repo clone alice/project src --depth 1

# Pass flags to git clone
$ gt repo clone alice/project -- --branch develop --recurse-submodules`,
		Annotations: map[string]string{"gt.arguments": cloneArguments},
		Args:        argsBeforeGitFlags(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			at := argsBeforeDash(cmd, args)
			args, opts.gitFlags = args[:at], args[at:]
			if opts.depth < 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--depth must be nonnegative; zero fetches full history"))
			}
			if a.options.repo != "" {
				return clierr.Wrap(clierr.Usage, errors.New("clone repository argument cannot be combined with --repo"))
			}
			if noUpstream {
				if cmd.Flags().Changed("upstream-remote-name") {
					return clierr.Wrap(clierr.Usage, errors.New("--no-upstream and --upstream-remote-name cannot be combined"))
				}
				opts.upstream = ""
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
			if len(args) == 2 {
				opts.directory = args[1]
			}
			if _, err := a.cloneRepository(cmd, t, item, opts); err != nil {
				return err
			}
			return a.jsonResult(cmd, item)
		},
	}
	command.Flags().IntVarP(&opts.depth, "depth", "d", 0, "Number of commits to fetch (0: full history)")
	command.Flags().StringVarP(&opts.upstream, "upstream-remote-name", "u", "upstream", "Remote name for the parent of a fork")
	command.Flags().BoolVar(&noUpstream, "no-upstream", false, "Do not add a remote for the parent of a fork or the source of a mirror")
	command.ValidArgsFunction = pathArgs(1, false, completeDirectories, a.completeRepos())
	return command
}

func (a *application) repoForkCommand() *cobra.Command {
	var opts services.ForkOptions
	var clone, addRemote bool
	var remoteName string
	command := &cobra.Command{
		Use:   "fork [<repository>] [-- <gitflags>...]",
		Short: "Fork a repository",
		Long: `Fork a repository for your account, or into an organization with --org. An existing fork is
reported instead of failing.

--clone clones the fork (Git flags can follow --). Inside a clone of the repository, --remote adds
the fork as "origin" and renames the current "origin" to "upstream".`,
		Example: `# Fork the repository of the current clone and add the fork as origin
$ gt repo fork --remote

# Fork a repository into an organization under another name
$ gt repo fork alice/project --org acme --fork-name project-fork

# Fork and clone a repository
$ gt repo fork alice/project --clone -- --depth 1`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		Args:        argsBeforeGitFlags(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			at := argsBeforeDash(cmd, args)
			args, gitFlags := args[:at], args[at:]
			switch {
			case clone && addRemote:
				return clierr.Wrap(clierr.Usage, errors.New("--clone and --remote cannot be combined"))
			case len(gitFlags) > 0 && !clone:
				return clierr.Wrap(clierr.Usage, errors.New("Git flags after -- require --clone"))
			case cmd.Flags().Changed("remote-name") && !addRemote:
				return clierr.Wrap(clierr.Usage, errors.New("--remote-name requires --remote"))
			}
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			owner := t.owner()
			if strings.EqualFold(owner, cmp.Or(opts.Organization, t.User)) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("cannot fork %s into %s: it already owns it; pass --org to fork it into an organization", t.Repo, owner))
			}
			rename := false
			if addRemote {
				if rename, err = a.checkForkRemote(cmd, t, remoteName); err != nil {
					return err
				}
			}
			fork, existed, err := (services.Repositories{Client: t.client}).Fork(cmd.Context(), t.Repo, opts)
			if err != nil {
				return err
			}
			if _, name, _ := strings.Cut(fork.FullName, "/"); existed && opts.Name != "" && !strings.EqualFold(name, opts.Name) {
				// Gitea allows one fork per owner, so --fork-name cannot give a second one.
				return fmt.Errorf("--fork-name %s was not applied: %s already forked %s as %s, and Gitea allows one fork per owner", opts.Name, cmp.Or(opts.Organization, t.User), t.Repo, fork.FullName)
			}
			if existed {
				a.warn(cmd, "%s already exists", fork.FullName)
			} else {
				a.done(cmd, "Forked %s to %s", t.Repo, fork.FullName)
			}
			if clone {
				if _, err := a.cloneRepository(cmd, t, fork, cloneOptions{gitFlags: gitFlags, upstream: "upstream"}); err != nil {
					return err
				}
			}
			if addRemote {
				if err := a.addForkRemote(cmd, t, fork, remoteName, rename); err != nil {
					return err
				}
			}
			return a.created(cmd, fork, fork.URL)
		},
	}
	command.Flags().StringVar(&opts.Organization, "org", "", "Fork into this organization")
	command.Flags().StringVar(&opts.Name, "fork-name", "", "Name of the fork (default: the repository's name)")
	command.Flags().BoolVar(&clone, "clone", false, "Clone the fork")
	command.Flags().BoolVar(&addRemote, "remote", false, "Add the fork as a remote of the current clone")
	command.Flags().StringVar(&remoteName, "remote-name", "origin", "Name of the fork's remote")
	_ = command.RegisterFlagCompletionFunc("org", a.completeOrgs())
	command.ValidArgsFunction = a.completeRepos()
	return command
}

// checkForkRemote reports, before anything is forked, whether the clone in the working directory
// can take the fork as the remote name, and whether a remote of that name already points to the
// forked repository and must be renamed to upstream first.
func (a *application) checkForkRemote(cmd *cobra.Command, t target, name string) (rename bool, err error) {
	ctx := cmd.Context()
	if inside, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree"); inside != "true" {
		return false, clierr.Wrap(clierr.Usage, fmt.Errorf("--remote must run inside a Git clone of %s", t.Repo))
	}
	if existing, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", name); err == nil {
		if !t.ownsRemote(existing) {
			return false, clierr.Wrap(clierr.Usage, fmt.Errorf("Git remote %s points elsewhere; pass --remote-name", name))
		}
		return true, nil
	}
	return false, nil
}

// addForkRemote adds fork as the remote name of the clone in the working directory, after renaming
// the remote of that name to upstream when rename is set.
func (a *application) addForkRemote(cmd *cobra.Command, t target, fork services.Repository, name string, rename bool) error {
	ctx := cmd.Context()
	if rename {
		if _, err := a.git(ctx, "remote", "rename", name, "upstream"); err != nil {
			return fmt.Errorf("forked to %s, but could not rename Git remote %s to upstream: %w", fork.FullName, name, err)
		}
		a.done(cmd, "Renamed Git remote %s to upstream", name)
	}
	url, _, err := cloneURLForProtocol(fork.CloneURL, fork.SSHURL, t.gitProtocol)
	if err != nil {
		return err
	}
	if _, err := a.git(ctx, "remote", "add", "--", name, url); err != nil {
		return fmt.Errorf("forked to %s, but could not add Git remote %s: %w", fork.FullName, name, err)
	}
	a.done(cmd, "Added Git remote %s for %s", name, fork.FullName)
	return nil
}

// argsBeforeGitFlags accepts between minimum and maximum arguments before --, and any Git flags after it.
func argsBeforeGitFlags(minimum, maximum int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if args = args[:argsBeforeDash(cmd, args)]; len(args) > maximum {
			return clierr.Wrap(clierr.Usage, &argumentError{problem: "too many arguments", cmd: cmd})
		}
		return usageArgs(cobra.MinimumNArgs(minimum))(cmd, args)
	}
}

// argsBeforeDash counts the arguments before --, which belong to gt; the rest go to Git.
func argsBeforeDash(cmd *cobra.Command, args []string) int {
	if at := cmd.ArgsLenAtDash(); at >= 0 {
		return at
	}
	return len(args)
}

// cloneOptions are the choices of repo clone, and of repo create and repo fork with --clone.
type cloneOptions struct {
	// directory is the clone's path; empty lets Git name it after the repository.
	directory string
	depth     int
	gitFlags  []string
	// upstream names the remote of a fork's parent; empty adds neither it nor a mirror's source.
	upstream string
}

// cloneRepository clones item with the configured Git protocol and adds fork and mirror remotes.
// It returns the path of the clone.
func (a *application) cloneRepository(cmd *cobra.Command, t target, item services.Repository, opts cloneOptions) (string, error) {
	directory, depth := opts.directory, opts.depth
	protocol := t.gitProtocol
	host := t.host
	if protocol == "ssh" {
		host = apiSSHHost(host, item.SSHURL)
	}
	hosts := map[string]settings.Host{t.Host: host}
	remote, remoteIsSSH, err := cloneURLForProtocol(item.CloneURL, item.SSHURL, protocol)
	if err != nil {
		return "", err
	}
	cloneHost, cloneRepo, err := settings.RepositoryURL(remote, hosts)
	if err != nil || cloneHost != t.Host || !strings.EqualFold(cloneRepo, item.FullName) || (protocol == "ssh") != remoteIsSSH {
		return "", errors.New("Gitea clone URL does not match the selected instance and repository; check the server root URL and configured ssh_host")
	}
	gitArgs := []string{"clone"}
	if depth > 0 {
		gitArgs = append(gitArgs, "--depth", fmt.Sprint(depth))
	}
	gitArgs = append(gitArgs, opts.gitFlags...)
	gitArgs = append(gitArgs, "--", remote)
	if directory != "" {
		gitArgs = append(gitArgs, directory)
	} else if directory = filepath.Base(strings.TrimSuffix(item.Name, ".git")); directory == "" || directory == "." || directory == "/" {
		directory = item.Name
	}
	credentialArgs, err := a.gitTransportArguments(t, remoteIsSSH)
	if err != nil {
		return "", err
	}
	gitArgs = append(credentialArgs, gitArgs...)
	if _, err := a.git(cmd.Context(), gitArgs...); err != nil {
		return "", err
	}
	clone := directory
	if !filepath.IsAbs(clone) {
		clone = filepath.Join(a.options.cwd, clone)
	}
	a.done(cmd, "Cloned %s into %s", item.FullName, directory)
	if item.Fork && item.Parent != nil && opts.upstream != "" {
		upstream, upstreamSSH, err := cloneURLForProtocol(item.Parent.CloneURL, item.Parent.SSHURL, protocol)
		if err != nil {
			return clone, errors.New("cloned the fork, but Gitea did not return a parent clone URL")
		}
		upstreamHost, upstreamRepo, err := settings.RepositoryURL(upstream, hosts)
		if err != nil || upstreamHost != t.Host || !strings.EqualFold(upstreamRepo, item.Parent.FullName) || (protocol == "ssh") != upstreamSSH {
			return clone, errors.New("cloned the fork, but its parent clone URL does not match the selected instance")
		}
		// Git flags or clone.defaultRemoteName may have renamed origin.
		origin, err := a.gitIn(cmd.Context(), clone, "remote")
		if err != nil {
			return clone, fmt.Errorf("cloned the fork, but could not list its remotes: %w", err)
		}
		origin, _, _ = strings.Cut(origin, "\n")
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", opts.upstream, upstream); err != nil {
			return clone, fmt.Errorf("cloned the fork, but could not add the %s remote: %w", opts.upstream, err)
		}
		// Fetched, so that the default branch does not track a missing upstream branch. The parent
		// is on the same instance with the same protocol, so the clone's credentials apply.
		fetchArgs := append(append([]string{}, credentialArgs...), "fetch", "--quiet")
		if depth > 0 {
			fetchArgs = append(fetchArgs, "--depth", fmt.Sprint(depth))
		}
		if _, err := a.gitIn(cmd.Context(), clone, append(fetchArgs, "--", opts.upstream)...); err != nil {
			return clone, fmt.Errorf("cloned the fork and added the %s remote, but could not fetch it: %w", opts.upstream, err)
		}
		// The default branch pulls from upstream, but pushes stay on the fork.
		if branch := item.Parent.DefaultBranch; branch != "" {
			for _, setting := range [][2]string{{"remote", opts.upstream}, {"merge", "refs/heads/" + branch}, {"pushRemote", origin}} {
				if _, err := a.gitIn(cmd.Context(), clone, "config", "branch."+branch+"."+setting[0], setting[1]); err != nil {
					return clone, fmt.Errorf("cloned the fork and added %s, but could not configure branch tracking: %w", opts.upstream, err)
				}
			}
		}
		a.done(cmd, "Added remote %s for %s; gt commands in the clone target it (change with gt repo set-default)", opts.upstream, item.Parent.FullName)
	}
	// The remote is named source because repository discovery prefers a remote named upstream.
	if source := mirrorSourceURL(item); source != "" && opts.upstream != "" {
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", "source", source); err != nil {
			return clone, fmt.Errorf("cloned the mirror, but could not add the source remote: %w", err)
		}
		a.done(cmd, "Added remote source for %s", source)
	}
	return clone, nil
}
