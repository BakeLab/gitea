// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
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
	if host.SSHHost != "" || apiSSHEndpoint(sshURL) == "" {
		return host
	}
	u, _ := settings.ParseRemote(sshURL)
	host.SSHHost = u.Host
	return host
}

// apiSSHEndpoint returns the normalized host:port of the API's ssh_url, or "" when it has none.
func apiSSHEndpoint(sshURL string) string {
	if !settings.RemoteIsSSH(sshURL) {
		return ""
	}
	u, err := settings.ParseRemote(sshURL)
	if err != nil {
		return ""
	}
	endpoint, _ := settings.SSHEndpoint(u.Host)
	return endpoint
}

// rememberSSHHost saves the SSH host learned from the API as the instance's ssh_host when it is not
// the web host on port 22, so that gt finds the repository of the clone's SSH remotes.
func (a *application) rememberSSHHost(cmd *cobra.Command, t target, sshHost string) {
	learned, err := settings.SSHEndpoint(sshHost)
	if standard, _ := settings.SSHEndpoint(settings.DefaultSSHHost(t.Host)); err != nil || learned == standard {
		return
	}
	snapshot, err := a.settings()
	if err != nil {
		return
	}
	if host, exists := snapshot.Hosts.Hosts[t.Host]; !exists {
		if instance, err := settings.ResolveHost(a.tokenInstance(), snapshot.Hosts.Hosts); a.deps.Getenv("GITEA_TOKEN") != "" && err == nil && instance == t.Host {
			a.warn(cmd, "gt commands in the clone need the SSH host of %s: set GITEA_INSTANCE_SSH_HOST=%s", t.Host, sshHost)
		}
		return
	} else if host.SSHHost != "" {
		return
	}
	saved := false
	err = a.deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		host, exists := s.Hosts.Hosts[t.Host]
		if !exists || host.SSHHost != "" {
			return nil
		}
		host.SSHHost, saved = sshHost, true
		s.Hosts.Hosts[t.Host] = host
		return nil
	})
	switch {
	case err != nil:
		a.warn(cmd, "Could not save the SSH host of %s: %v; run gt config set ssh_host %s --host %s", t.Host, err, sshHost, t.Host)
	case saved:
		a.done(cmd, "Set ssh_host of %s to %s", t.Host, sshHost)
	}
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
	var noUpstream, findInstance bool
	command := &cobra.Command{
		Use:   "clone <repository> [<directory>] [-- <gitflags>...]",
		Short: "Clone a repository locally",
		Long: `Clone a repository with gt's credentials, over the protocol of a URL argument, else the
configured Git protocol. Git flags can follow --. A fork's parent is added as the "upstream"
remote (or the --upstream-remote-name one), and gt commands in the clone then target the parent;
with remote names other than upstream and origin, it is saved as with gt repo set-default. A pull
mirror's source is added as the "source" remote; gt commands keep targeting the mirror. An HTTPS
clone keeps the instance's stored TLS client certificate and --insecure setting in its Git
configuration; GITEA_INSTANCE_INSECURE applies to the clone only. With --find-instance, when no
instance is selected and accounts exist on several, the repository is looked up on each of them; if
more than one has it, gt asks which to clone from, or fails without prompts. The lookup sends the
repository name to every instance with an account.`,
		Example: `# Clone a repository into ./project
$ gt repo clone alice/project

# Clone into another directory with the last commit only
$ gt repo clone alice/project src --depth 1

# Clone from whichever instance with an account has the repository
$ gt repo clone alice/project --find-instance

# Clone over SSH whatever the configured Git protocol
$ gt repo clone git@gitea.com:alice/project.git

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
				return clierr.Wrap(clierr.Usage, errors.New("a repository argument cannot be combined with --repo"))
			}
			if findInstance && a.options.host != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--find-instance cannot be combined with --host"))
			}
			if noUpstream {
				if cmd.Flags().Changed("upstream-remote-name") {
					return clierr.Wrap(clierr.Usage, errors.New("--no-upstream cannot be combined with --upstream-remote-name"))
				}
				opts.upstream = ""
			} else if err := a.checkUpstreamRemoteName(cmd, opts); err != nil {
				return err
			}
			a.options.repo, a.options.repoArgument, a.options.ignoresRemote = args[0], true, true
			t, err := a.resolve(cmd.Context(), true)
			// An SSH URL on the web host of an instance without ssh_host is cloned when the API
			// confirms its SSH endpoint.
			unknownSSH, isSSH := errors.AsType[*settings.SSHRemoteError](err)
			if isSSH && unknownSSH.Instance != "" {
				if u, parseErr := settings.ParseRemote(args[0]); parseErr == nil {
					a.options.repo = unknownSSH.Instance + "/" + strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
					t, err = a.resolve(cmd.Context(), true)
				}
			}
			var item services.Repository
			required, ambiguous := errors.AsType[*instanceRequiredError](err)
			switch {
			case ambiguous && required.loggedIn && findInstance:
				t, item, err = a.findRepository(cmd, required.instances)
			case ambiguous && required.loggedIn:
				err = clierr.Wrap(clierr.Usage, fmt.Errorf("%w; --find-instance looks the repository up on each of them", required))
			case err == nil:
				item, err = (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
			}
			if err != nil {
				return err
			}
			if unknownSSH != nil && apiSSHEndpoint(item.SSHURL) != unknownSSH.Endpoint {
				return clierr.Wrap(clierr.Usage, unknownSSH)
			}
			if item.Fork && item.Parent != nil && opts.upstream != "" && !cmd.Flags().Changed("upstream-remote-name") {
				if origin := a.cloneRemoteName(cmd.Context(), opts.gitFlags); origin == opts.upstream {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("cloning the fork as Git remote %s requires another --upstream-remote-name", origin))
				}
			}
			if len(args) == 2 {
				opts.directory = args[1]
			}
			if settings.RemoteIsSSH(args[0]) {
				t.gitProtocol = "ssh"
			} else if strings.Contains(args[0], "://") {
				t.gitProtocol = "https"
			}
			if _, err := a.cloneRepository(cmd, t, item, opts); err != nil {
				return err
			}
			return a.jsonResult(cmd, item)
		},
	}
	command.Flags().IntVarP(&opts.depth, "depth", "d", 0, "Number of commits to fetch (0: full history)")
	command.Flags().StringVarP(&opts.upstream, "upstream-remote-name", "u", "upstream", "Remote name for the parent of a fork")
	command.Flags().BoolVar(&findInstance, "find-instance", false, "Look the repository up on each instance with an account when none is selected")
	command.Flags().BoolVar(&noUpstream, "no-upstream", false, "Do not add a remote for the parent of a fork or the source of a mirror")
	command.ValidArgsFunction = pathArgs(1, false, completeDirectories, a.completeRepos())
	return command
}

// findRepository looks up the repository argument on each of instances when none is selected,
// and picks the instance that has it, asking when several do. The lookups never prompt.
func (a *application) findRepository(cmd *cobra.Command, instances []string) (target, services.Repository, error) {
	type match struct {
		target target
		item   services.Repository
	}
	var matches []match
	var failures []error
	for _, instance := range instances {
		if err := cmd.Context().Err(); err != nil {
			return target{}, services.Repository{}, err
		}
		probe := &application{deps: a.deps, options: a.options, dryRunRequests: a.dryRunRequests}
		probe.options.host, probe.options.noInput = instance, true
		t, err := probe.resolve(cmd.Context(), true)
		if err == nil {
			var item services.Repository
			if item, err = (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo); err == nil {
				matches = append(matches, match{t, item})
				continue
			}
		}
		if !services.IsNotFound(err) {
			if !strings.Contains(err.Error(), instance) {
				err = fmt.Errorf("%s: %w", instance, err)
			}
			failures = append(failures, err)
		}
	}
	repo := a.options.repo
	if len(matches) == 0 {
		if len(failures) > 0 {
			return target{}, services.Repository{}, failures[0]
		}
		return target{}, services.Repository{}, fmt.Errorf("repository %s not found on any instance (instances: %s)", repo, strings.Join(instances, ", "))
	}
	for _, failure := range failures {
		a.warn(cmd, "Could not look up %s: %v", repo, failure)
	}
	chosen := matches[0]
	if len(matches) > 1 {
		locations := make([]string, len(matches))
		for i, m := range matches {
			locations[i] = m.target.Host + "/" + m.item.FullName
		}
		chooser, ok := a.chooser()
		if !ok {
			return target{}, services.Repository{}, clierr.Wrap(clierr.Usage, fmt.Errorf("repository %s exists on several instances; select one with --host (%s)", repo, strings.Join(locations, ", ")))
		}
		choices := make([]terminal.Choice, len(locations))
		for i, location := range locations {
			choices[i] = terminal.Choice{Label: location, Value: location}
		}
		location, err := chooser.Choose(cmd.Context(), "Which instance should "+repo+" be cloned from?", choices, "")
		if err != nil {
			return target{}, services.Repository{}, err
		}
		chosen = matches[slices.Index(locations, location)]
	}
	a.mu.Lock()
	a.resolved = chosen.target
	a.mu.Unlock()
	return chosen.target, chosen.item, nil
}

// checkUpstreamRemoteName refuses an --upstream-remote-name that Git rejects, or that the given
// one names the fork itself.
func (a *application) checkUpstreamRemoteName(cmd *cobra.Command, opts cloneOptions) error {
	if opts.upstream == "" {
		return clierr.Wrap(clierr.Usage, errors.New("--upstream-remote-name requires a name; use --no-upstream"))
	}
	if err := validateRemoteName("upstream-remote-name", opts.upstream); err != nil {
		return err
	}
	if !cmd.Flags().Changed("upstream-remote-name") {
		return nil // The default only matters for a fork, which is known once the repository is.
	}
	if origin := a.cloneRemoteName(cmd.Context(), opts.gitFlags); opts.upstream == origin {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("--upstream-remote-name cannot be the fork's remote name %s", origin))
	}
	return nil
}

// cloneRemoteName is the name git clone gives the remote of the cloned repository: that of -o in
// gitFlags, else clone.defaultRemoteName of -c in gitFlags or of the configuration, else origin.
func (a *application) cloneRemoteName(ctx context.Context, gitFlags []string) string {
	var origin, configured string
	for i := 0; i < len(gitFlags); i++ {
		var option, value string
		switch flag := gitFlags[i]; {
		case slices.Contains([]string{"-o", "--origin", "-c", "--config"}, flag) && i+1 < len(gitFlags):
			i++
			option, value = flag, gitFlags[i]
		case strings.HasPrefix(flag, "--origin="), strings.HasPrefix(flag, "--config="):
			option, value, _ = strings.Cut(flag, "=")
		case strings.HasPrefix(flag, "-o"), strings.HasPrefix(flag, "-c"):
			option, value = flag[:2], flag[2:]
		}
		switch option {
		case "-o", "--origin":
			origin = value
		case "-c", "--config":
			if key, name, ok := strings.Cut(value, "="); ok && strings.EqualFold(key, "clone.defaultRemoteName") {
				configured = name
			}
		}
	}
	if origin != "" || configured != "" {
		return cmp.Or(origin, configured)
	}
	// git clone reads no configuration of the repository it may run in.
	scoped, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "config", "--show-scope", "--get-all", "clone.defaultRemoteName")
	for line := range strings.Lines(scoped) {
		if scope, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t"); ok && scope != "local" && scope != "worktree" {
			configured = name
		}
	}
	return cmp.Or(configured, "origin")
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

--clone clones the fork (Git flags can follow --). Inside a clone of the repository, --add-remote
adds the fork as "origin" and renames the current "origin" to "upstream".`,
		Example: `# Fork the repository of the current clone and add the fork as origin
$ gt repo fork --add-remote

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
				return clierr.Wrap(clierr.Usage, errors.New("--clone cannot be combined with --add-remote"))
			case len(gitFlags) > 0 && !clone:
				return clierr.Wrap(clierr.Usage, errors.New("Git flags after -- require --clone"))
			case cmd.Flags().Changed("remote-name") && !addRemote:
				return clierr.Wrap(clierr.Usage, errors.New("--remote-name requires --add-remote"))
			}
			if err := validateRemoteName("remote-name", remoteName); err != nil {
				return err
			}
			if cmd.Flags().Changed("fork-name") {
				if err := validateRepositoryName(opts.Name); err != nil {
					return err
				}
			}
			if clone && a.cloneRemoteName(cmd.Context(), gitFlags) == "upstream" {
				return clierr.Wrap(clierr.Usage, errors.New("repo fork --clone requires a Git remote name for the fork other than upstream, the name of its parent's remote"))
			}
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			owner, forkOwner := t.owner(), cmp.Or(opts.Organization, t.login())
			if strings.EqualFold(owner, forkOwner) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("cannot fork %s into %s: it already owns it; pass --org to fork it into an organization", t.Repo, owner))
			}
			rename, current := false, ""
			if addRemote {
				if rename, current, err = a.checkForkRemote(cmd, t, remoteName, forkOwner); err != nil {
					return err
				}
			}
			fork, existed, err := (services.Repositories{Client: t.client}).Fork(cmd.Context(), t.Repo, opts)
			if err != nil {
				return err
			}
			if _, name, _ := strings.Cut(fork.FullName, "/"); existed && opts.Name != "" && !strings.EqualFold(name, opts.Name) {
				// Gitea allows one fork per owner, so --fork-name cannot give a second one.
				return fmt.Errorf("--fork-name %s was not applied: %s already forked %s as %s, and Gitea allows one fork per owner", opts.Name, forkOwner, t.Repo, fork.FullName)
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
				if err := a.addForkRemote(cmd, t, fork, remoteName, rename, current); err != nil {
					return err
				}
			}
			return a.created(cmd, fork, fork.URL)
		},
	}
	command.Flags().StringVar(&opts.Organization, "org", "", "Fork into this organization")
	command.Flags().StringVar(&opts.Name, "fork-name", "", "Name of the fork (default: the repository's name)")
	command.Flags().BoolVar(&clone, "clone", false, "Clone the fork")
	command.Flags().BoolVar(&addRemote, "add-remote", false, "Add the fork as a remote of the current clone")
	command.Flags().StringVar(&remoteName, "remote-name", "origin", "Name of the fork's remote")
	_ = command.RegisterFlagCompletionFunc("org", a.completeOrgs())
	command.ValidArgsFunction = a.completeRepos()
	return command
}

// checkForkRemote reports, before anything is forked, whether the clone in the working directory
// can take the fork as the remote name: rename when a remote of that name points to the forked
// repository and must be renamed to upstream first, and current when it already points to a fork
// of it by owner, such as the fork of an earlier run.
func (a *application) checkForkRemote(cmd *cobra.Command, t target, name, owner string) (rename bool, current string, err error) {
	ctx := cmd.Context()
	notAClone := clierr.Wrap(clierr.Usage, fmt.Errorf("--add-remote requires a Git clone of %s", t.Repo))
	if !a.insideClone(ctx) {
		return false, "", notAClone
	}
	probe := func(args ...string) (string, error) { return a.gitProbe().Output(ctx, a.options.cwd, false, args...) }
	cloned := func() bool {
		remotes, _ := probe("remote")
		return slices.ContainsFunc(strings.Fields(remotes), func(remote string) bool {
			url, err := probe("remote", "get-url", remote)
			return err == nil && t.ownsRemote(url)
		})
	}
	existing, err := probe("remote", "get-url", name)
	if err != nil {
		if !cloned() {
			return false, "", notAClone
		}
		return false, "", nil
	}
	if t.ownsRemote(existing) {
		if name == "upstream" {
			return false, "", clierr.Wrap(clierr.Usage, fmt.Errorf("Git remote upstream points to %s; pass another --remote-name", t.Repo))
		}
		if _, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", "upstream"); err == nil {
			return false, "", clierr.Wrap(clierr.Usage, fmt.Errorf("Git remote %s points to %s and cannot be renamed to upstream, which exists; pass --remote-name", name, t.Repo))
		}
		return true, "", nil
	}
	host, repo, err := settings.RemoteRepository(existing, t.hosts())
	onInstance := err == nil && host == t.Host
	if onInstance && strings.EqualFold(strings.Split(repo, "/")[0], owner) {
		fork, err := (services.Repositories{Client: t.client}).View(ctx, repo)
		if err != nil && !services.IsNotFound(err) {
			return false, "", err
		}
		if err == nil && fork.Parent != nil && strings.EqualFold(fork.Parent.FullName, t.Repo) {
			return false, repo, nil
		}
	}
	if !cloned() {
		return false, "", notAClone
	}
	pointsTo := settings.WithoutCredentials(existing)
	if onInstance {
		pointsTo = repo
	}
	return false, "", clierr.Wrap(clierr.Usage, fmt.Errorf("Git remote %s points to %s; pass --remote-name", name, pointsTo))
}

// addForkRemote adds fork as the remote name of the clone in the working directory, after renaming
// the remote of that name to upstream when rename is set. A remote current that already points to
// the fork is kept.
func (a *application) addForkRemote(cmd *cobra.Command, t target, fork services.Repository, name string, rename bool, current string) error {
	ctx := cmd.Context()
	if current != "" {
		if !strings.EqualFold(current, fork.FullName) {
			return fmt.Errorf("Git remote %s points to %s, not the fork %s; pass --remote-name", name, current, fork.FullName)
		}
		a.warn(cmd, "Git remote %s already points to %s", name, fork.FullName)
		return nil
	}
	if rename {
		if _, err := a.git(ctx, "remote", "rename", "--", name, "upstream"); err != nil {
			return fmt.Errorf("forked to %s, but could not rename Git remote %s to upstream: %w", fork.FullName, name, err)
		}
		if err := git.RenameBaseRemote(ctx, a.gitProbe(), a.options.cwd, name, "upstream"); err != nil {
			return fmt.Errorf("renamed Git remote %s to upstream, but could not update the branches that pr commands recorded for it: %w", name, err)
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
		return nonBlankArgs(cobra.MinimumNArgs(minimum), "repository", "directory")(cmd, args)
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
	// directory is the clone's path; empty names it after the repository, as Git does.
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
	if directory == "" {
		if directory = filepath.Base(strings.TrimSuffix(item.Name, ".git")); directory == "" || directory == "." || directory == "/" {
			directory = item.Name
		}
		if slices.ContainsFunc(opts.gitFlags, func(flag string) bool { return flag == "--bare" || flag == "--mirror" }) {
			directory += ".git"
		}
	}
	gitArgs = append(gitArgs, "--", remote)
	if directory != "" {
		// Named explicitly, so that the setup below runs where Git cloned to.
		gitArgs = append(gitArgs, directory)
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
	if !remoteIsSSH {
		stored, err := a.storedHost(t.Host)
		if err == nil {
			err = git.SetupTLS(cmd.Context(), a.deps.Git, clone, true, t.Host, stored.ClientCert, stored.ClientKey, stored.Insecure)
		}
		if err != nil {
			return clone, fmt.Errorf("cloned the repository, but could not configure its TLS settings: %w", err)
		}
	}
	if host.SSHHost != t.host.SSHHost {
		a.rememberSSHHost(cmd, t, host.SSHHost)
	}
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
		// Discovery tells the parent from the fork by the names upstream and origin only.
		if opts.upstream != "upstream" || origin != "origin" {
			if _, err := a.gitIn(cmd.Context(), clone, "config", "--local", "gt.repository", t.Host+"/"+item.Parent.FullName); err != nil {
				return clone, fmt.Errorf("cloned the fork and added %s, but could not save it as the default repository: %w", opts.upstream, err)
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
