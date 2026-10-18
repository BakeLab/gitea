// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

func (a *application) addRepositoryMutations(group *cobra.Command) {
	group.AddCommand(a.repoCreateCommand(), a.repoMigrateCommand(), a.repoForkCommand(), a.repoCloneCommand(), a.repoEditCommand())
	group.AddCommand(a.repoArchiveCommand(true), a.repoArchiveCommand(false), a.repoRenameCommand(), a.repoDeleteCommand())
}

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
	{"delete-branch-on-merge", "Delete head branches after merging pull requests", func(e *services.RepositoryEdit) **bool { return &e.DeleteBranch }},
	{"archived", "Archive the repository (=false unarchives it)", func(e *services.RepositoryEdit) **bool { return &e.Archived }},
}

func (a *application) repoEditCommand() *cobra.Command {
	var name, website, defaultBranch, description, visibility, mergeStyle string
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
$ gt repo edit --default-merge-style squash --add-topic cli --add-topic go`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		Args:        usageArgs(cobra.MaximumNArgs(1)),
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
			}{{"name", &name, &options.Name}, {"default-branch", &defaultBranch, &options.DefaultBranch}, {"default-merge-style", &mergeStyle, &options.MergeStyle}} {
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
				options.Description = &description
			}
			if changed("visibility") {
				if changed("private") {
					return clierr.Wrap(clierr.Usage, errors.New("--visibility and --private cannot be combined"))
				}
				private := visibility == "private"
				options.Private = &private
			}
			if err := requireNames("topic", "", addTopics, removeTopics); err != nil {
				return err
			}
			if options == (services.RepositoryEdit{}) && len(addTopics)+len(removeTopics) == 0 {
				return clierr.Wrap(clierr.Usage, errors.New("supply a setting to change; see gt repo edit --help"))
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Repositories{Client: t.client}
			if options.DefaultBranch != nil {
				if _, err := (services.Branches{Client: t.client}).View(cmd.Context(), t.Repo, defaultBranch); err != nil {
					return services.Classify(err, "branch", strconv.Quote(defaultBranch), t.Repo)
				}
			}
			// Gitea refuses topic changes on an archived repository, so archiving comes last.
			repo, archive := t.Repo, options.Archived != nil && *options.Archived
			if archive {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Archive " + t.Repo + "? It becomes read-only."}); err != nil {
					return err
				}
				options.Archived = nil
			}
			if options != (services.RepositoryEdit{}) {
				item, err := service.Edit(cmd.Context(), t.Repo, options)
				if err != nil {
					return err
				}
				repo = item.FullName
			}
			for _, topic := range addTopics {
				if err := service.AddTopic(cmd.Context(), repo, topic); err != nil {
					return fmt.Errorf("add topic %q: %w", topic, err)
				}
			}
			for _, topic := range removeTopics {
				if err := service.RemoveTopic(cmd.Context(), repo, topic); err != nil {
					return fmt.Errorf("remove topic %q: %w", topic, err)
				}
			}
			if archive {
				if _, err := service.Edit(cmd.Context(), repo, services.RepositoryEdit{Archived: &archive}); err != nil {
					return err
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
	flags.StringVar(&name, "name", "", "Rename the repository")
	_ = flags.MarkHidden("name")
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
	_ = flags.MarkHidden("archived")
	_ = command.RegisterFlagCompletionFunc("default-branch", a.completeBranches())
	command.ValidArgsFunction = a.completeRepos()
	return command
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
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
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
			owner, _, _ := strings.Cut(t.Repo, "/")
			if strings.EqualFold(owner, cmp.Or(opts.Organization, t.User)) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("cannot fork %s into %s: it already owns it; pass --org to fork it into an organization", t.Repo, owner))
			}
			fork, existed, err := (services.Repositories{Client: t.client}).Fork(cmd.Context(), t.Repo, opts)
			if err != nil {
				return err
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
				if err := a.addForkRemote(cmd, t, fork, remoteName); err != nil {
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

// addForkRemote adds fork as the remote name of the clone in the working directory. A remote of
// that name that points to the forked repository is renamed to upstream first.
func (a *application) addForkRemote(cmd *cobra.Command, t target, fork services.Repository, name string) error {
	ctx := cmd.Context()
	if inside, _ := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree"); inside != "true" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("forked to %s, but --remote must run inside a Git clone of %s", fork.FullName, t.Repo))
	}
	if existing, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", name); err == nil {
		if !t.ownsRemote(existing) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("forked to %s, but Git remote %s points elsewhere; pass --remote-name", fork.FullName, name))
		}
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
		if _, err := a.gitIn(cmd.Context(), clone, "remote", "add", "--", opts.upstream, upstream); err != nil {
			return clone, fmt.Errorf("cloned the fork, but could not add the %s remote: %w", opts.upstream, err)
		}
		// The default branch pulls from upstream, but pushes stay on the fork.
		if branch := item.Parent.DefaultBranch; branch != "" {
			for _, setting := range [][2]string{{"remote", opts.upstream}, {"merge", "refs/heads/" + branch}, {"pushRemote", "origin"}} {
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

// inferredRepository words where a repository that no argument or --repo named came from, as in
// " (from git remote upstream)", for confirmations. Irreversible commands refuse --yes for it.
func (a *application) inferredRepository(t target, verb string, refuseYes bool) (string, error) {
	if t.RepoSource == "--repo" {
		return "", nil
	}
	source := strings.Replace(t.RepoSource, "remote ", "git remote ", 1)
	if refuseYes && a.options.yes {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("--yes requires an explicit repository, not one inferred from %s: gt repo %s %s --yes", source, verb, t.Repo))
	}
	return " (from " + source + ")", nil
}

// repoArchiveCommand builds repo archive, or repo unarchive.
func (a *application) repoArchiveCommand(archive bool) *cobra.Command {
	command := &cobra.Command{
		Use:   "archive [<repository>]",
		Short: "Archive a repository",
		Long: `Archive a repository. An archived repository is read-only: nobody can push to it or change its
issues, pull requests, releases or wiki until it is unarchived with gt repo unarchive.`,
		Example: `# Archive a repository
$ gt repo archive alice/old-project

# Archive without a prompt; scripts must name the repository
$ gt repo archive alice/old-project --yes`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments, "gt.confirm": "true"},
		Args:        usageArgs(cobra.MaximumNArgs(1)),
	}
	if !archive {
		command.Use, command.Short = "unarchive [<repository>]", "Unarchive a repository"
		command.Long = "Unarchive an archived repository, so that it accepts pushes and changes again."
		command.Example = "$ gt repo unarchive alice/old-project"
		delete(command.Annotations, "gt.confirm")
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if err := a.repositoryArgument(args); err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		inferred, err := a.inferredRepository(t, "archive", archive)
		if err != nil {
			return err
		}
		service := services.Repositories{Client: t.client}
		item, err := service.View(cmd.Context(), t.Repo)
		if err != nil {
			return err
		}
		if item.Archived == archive {
			if archive {
				a.warn(cmd, "%s is already archived", item.FullName)
			} else {
				a.warn(cmd, "%s is not archived", item.FullName)
			}
			return a.jsonResult(cmd, item)
		}
		if archive {
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Archive " + t.Repo + inferred + "? It becomes read-only.", NonTTY: "archive " + t.Repo}); err != nil {
				return err
			}
		}
		item, err = service.Edit(cmd.Context(), t.Repo, services.RepositoryEdit{Archived: &archive})
		if err != nil {
			return err
		}
		if archive {
			a.done(cmd, "Archived repository %s", item.FullName)
		} else {
			a.done(cmd, "Unarchived repository %s", item.FullName)
		}
		return a.jsonResult(cmd, item)
	}
	command.ValidArgsFunction = a.completeRepos()
	return command
}

func (a *application) repoRenameCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename a repository",
		Long:  "Rename a repository; its owner stays the same. Gitea redirects the old name to the new one.",
		Example: `# Rename the repository of the current clone
$ gt repo rename new-name

# Rename another repository
$ gt repo rename new-name -R alice/old-name`,
		Annotations: map[string]string{"gt.confirm": "true"},
		Args:        argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if err := validateRepositoryName(name); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			owner, current, _ := strings.Cut(t.Repo, "/")
			if current == name {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is already named %s", t.Repo, name))
			}
			inferred, err := a.inferredRepository(t, "rename", false)
			if err != nil {
				return err
			}
			if inferred != "" {
				if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Rename " + t.Repo + inferred + " to " + owner + "/" + name + "?", DefaultYes: true, NonTTY: "rename " + t.Repo}); err != nil {
					return err
				}
			}
			item, err := (services.Repositories{Client: t.client}).Edit(cmd.Context(), t.Repo, services.RepositoryEdit{Name: &name})
			if err != nil {
				return err
			}
			a.done(cmd, "Renamed repository %s to %s", t.Repo, item.FullName)
			if remote, ok := strings.CutPrefix(t.RepoSource, "remote "); ok {
				a.warn(cmd, "Git remote %s still points to %s; update it with git remote set-url", remote, t.Repo)
			}
			return a.jsonResult(cmd, item)
		},
	}
}

func (a *application) repoDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete [<repository>]",
		Short: "Delete a repository",
		Long:  "Delete a repository with its issues, pull requests, releases and wiki. This cannot be undone.",
		Example: `# Delete a repository after typing its name
$ gt repo delete alice/old-project

# Delete without a prompt
$ gt repo delete alice/old-project --yes`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments, "gt.confirm": "true", "gt.text": "repository deletion does not return a resource"},
		Args:        usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			inferred, err := a.inferredRepository(t, "delete", true)
			if err != nil {
				return err
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + t.Repo + inferred + "? This cannot be undone.", Typed: t.Repo, NonTTY: "delete " + t.Repo}); err != nil {
				return err
			}
			if err := (services.Repositories{Client: t.client}).Delete(cmd.Context(), t.Repo); err != nil {
				return err
			}
			a.done(cmd, "Deleted repository %s", t.Repo)
			return nil
		},
	}
	command.ValidArgsFunction = a.completeRepos()
	return command
}
