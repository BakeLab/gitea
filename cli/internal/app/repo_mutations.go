// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"

	"github.com/spf13/cobra"
)

func (a *application) addRepositoryMutations(group *cobra.Command) {
	group.AddCommand(a.repoCreateCommand(), a.repoMigrateCommand(), a.repoForkCommand(), a.repoCloneCommand(), a.repoEditCommand())
	group.AddCommand(a.repoArchiveCommand(true), a.repoArchiveCommand(false), a.repoRenameCommand(), a.repoTransferCommand(), a.repoDeleteCommand(), a.deployKeyCommand())
}

// cloneArguments documents the <repository> argument of repo clone, which is required.
const cloneArguments = "A repository can be supplied as NAME (one of yours), OWNER/NAME, HOST/OWNER/NAME or a URL."

// inferredRepository words where a repository that no argument, --repo or GITEA_CLI_REPO named came from, as in
// " (from git remote upstream)", for confirmations. Irreversible commands refuse --yes for it.
func (a *application) inferredRepository(t target, verb string, refuseYes bool) (string, error) {
	switch t.RepoSource {
	case "--repo", "argument", "GITEA_CLI_REPO":
		return "", nil
	}
	source := strings.Replace(t.RepoSource, "remote ", "git remote ", 1)
	if refuseYes && a.options.yes {
		return "", clierr.Wrap(clierr.Usage, fmt.Errorf("--yes requires an explicit repository, not one inferred from %s: gt repo %s %s --yes", source, verb, a.explicitRepository(t)))
	}
	return " (from " + source + ")", nil
}

// explicitRepository names t's repository for a suggested command that acts on the same one: as
// HOST/OWNER/REPO off the default instance, since OWNER/REPO alone names the default instance's,
// and with the --user and --sudo that select the account.
func (a *application) explicitRepository(t target) string {
	repository := t.Repo
	if t.Host != "" && !a.isDefaultHost(t.Host) {
		repository = strings.TrimSuffix(t.Host, "/") + "/" + t.Repo
	}
	if t.explicitUser() {
		repository += " --user " + t.User
	}
	if a.options.sudo != "" {
		repository += " --sudo " + a.options.sudo
	}
	return repository
}

// repoArchiveCommand builds repo archive, or repo unarchive.
func (a *application) repoArchiveCommand(archive bool) *cobra.Command {
	command := &cobra.Command{
		Use:   "archive [<repository>...]",
		Short: "Archive a repository",
		Long: `Archive a repository. An archived repository is read-only: nobody can push to it or change its
issues, pull requests, releases or wiki until it is unarchived with gt repo unarchive.`,
		Example: `# Archive a repository
$ gt repo archive alice/old-project

# Archive without a prompt; scripts must name the repository
$ gt repo archive alice/old-project --yes

# Archive several repositories after one confirmation
$ gt repo archive acme/legacy-api acme/legacy-web`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments, "gt.confirm": "true"},
		Args:        nonBlankArgs(cobra.ArbitraryArgs, "repository"),
	}
	if !archive {
		command.Use, command.Short = "unarchive [<repository>...]", "Unarchive a repository"
		command.Long = "Unarchive an archived repository, so that it accepts pushes and changes again."
		command.Example = "$ gt repo unarchive alice/old-project"
		delete(command.Annotations, "gt.confirm")
	}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return a.archiveRepositories(cmd, args, archive)
		}
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
			a.warnArchived(cmd, item)
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

// archiveRepositories archives or unarchives each named repository, after one confirmation.
func (a *application) archiveRepositories(cmd *cobra.Command, repositories []string, archive bool) error {
	if a.options.repo != "" {
		return clierr.Wrap(clierr.Usage, errors.New("repository arguments cannot be combined with --repo"))
	}
	verb := "Unarchived"
	if archive {
		verb = "Archived"
		prompt := fmt.Sprintf("Archive %d repositories (%s)? They become read-only.", len(repositories), strings.Join(repositories, ", "))
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: prompt, NonTTY: "archive " + strings.Join(repositories, ", ")}); err != nil {
			return err
		}
	}
	snapshot, err := a.settings()
	if err != nil {
		return err
	}
	hosts, err := a.repositoryHosts(snapshot.Hosts.Hosts)
	if err != nil {
		return err
	}
	// Each instance is connected to once, however many of its repositories are named.
	clients := map[string]target{}
	unchanged := map[string]bool{}
	results, failures := applyBatch(cmd, repositories, func(repository string) string { return repository }, func(repository string) (services.Repository, error) {
		ref, err := settings.ParseRepository(repository, hosts)
		if err != nil {
			return services.Repository{}, clierr.Wrap(clierr.Usage, err)
		}
		t, connected := clients[ref.Host]
		switch {
		case !connected:
			if t, err = a.resolveRequest(cmd.Context(), targetRequest{requireRepo: true, authenticate: true, repo: repository, repoSource: "argument"}); err != nil {
				return services.Repository{}, err
			}
			clients[ref.Host], clients[t.Host] = t, t
		case ref.Owner != "":
			t.Repo = ref.Slug()
		default:
			if t.Repo, err = a.accountRepo(t, ref.Name); err != nil {
				return services.Repository{}, err
			}
		}
		service := services.Repositories{Client: t.client}
		item, err := service.View(cmd.Context(), t.Repo)
		if err != nil || item.Archived == archive {
			unchanged[item.FullName] = err == nil
			return item, err
		}
		return service.Edit(cmd.Context(), t.Repo, services.RepositoryEdit{Archived: &archive})
	}, func(item services.Repository) {
		if unchanged[item.FullName] {
			a.warnArchived(cmd, item)
			return
		}
		a.done(cmd, "%s repository %s", verb, item.FullName)
	})
	return errors.Join(append(failures, writeBatchJSON(a, cmd, len(repositories), results))...)
}

// warnArchived tells that item is already in the archived state asked for.
func (a *application) warnArchived(cmd *cobra.Command, item services.Repository) {
	if item.Archived {
		a.warn(cmd, "%s is already archived", item.FullName)
	} else {
		a.warn(cmd, "%s is not archived", item.FullName)
	}
}

func (a *application) repoRenameCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename a repository",
		Long: `Rename a repository; its owner stays the same. The Git remote that selected the repository, or
the first remote pointing to it when gt repo set-default selected it, and a default repository saved
with gt repo set-default for it, are updated. Other Git remotes keep working through Gitea's redirect
from the old name, but gt commands there do not.`,
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
			owner, current := t.ownerName()
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
			a.repositoryMoved(cmd, t, item.FullName)
			return a.jsonResult(cmd, item)
		},
	}
}

// repositoryMoved points the Git remote of the clone that selected t, or the first one pointing to t
// when its default repository did, and that default repository when it names t, to fullName, the new
// name of t's repository.
func (a *application) repositoryMoved(cmd *cobra.Command, t target, fullName string) {
	if a.options.dryRun {
		return // a dry run changes nothing in the clone either
	}
	ctx := cmd.Context()
	remote, ok := strings.CutPrefix(t.RepoSource, "remote ")
	if t.RepoSource == "git config gt.repository" {
		for _, candidate := range a.gitRemotes(ctx) {
			if t.ownsRemote(candidate[1]) {
				remote, ok = candidate[0], true
				break
			}
		}
	}
	if ok {
		probe := func(args ...string) (string, error) { return a.gitProbe().Output(ctx, a.options.cwd, false, args...) }
		// The configured URL keeps a url.<base>.insteadOf shorthand; one that hides the repository's
		// owner is replaced by the URL Git expands it to.
		moved := func(key string, getURL ...string) (string, bool) {
			raw, err := probe("config", "--get", "remote."+remote+"."+key)
			if err != nil {
				return "", false
			}
			if moved, ok := movedRemoteURL(raw, t.Repo, fullName); ok {
				return moved, true
			}
			expanded, err := probe(append(append([]string{"remote", "get-url"}, getURL...), remote)...)
			moved, ok := movedRemoteURL(expanded, t.Repo, fullName)
			return moved, err == nil && ok
		}
		if push, ok := moved("pushurl", "--push"); ok {
			if _, err := a.git(ctx, "remote", "set-url", "--push", "--", remote, push); err != nil {
				a.warn(cmd, "Git remote %s still pushes to %s; update it with git remote set-url --push", remote, t.Repo)
			}
		}
		url, ok := moved("url")
		var err error
		if ok {
			_, err = a.git(ctx, "remote", "set-url", "--", remote, url)
		}
		if err != nil || !ok {
			a.warn(cmd, "Git remote %s still points to %s; update it with git remote set-url", remote, t.Repo)
		} else {
			a.done(cmd, "Updated Git remote %s to %s", remote, fullName)
		}
	}
	saved, _ := a.gitProbe().Output(cmd.Context(), a.options.cwd, false, "config", "--local", "--get", "gt.repository")
	if t.RepoSource == "git config gt.repository" || strings.EqualFold(saved, t.Host+"/"+t.Repo) {
		// gt's own setting; the old name would fail with HTTP 301 from now on.
		if _, err := a.git(cmd.Context(), "config", "--local", "gt.repository", t.Host+"/"+fullName); err != nil {
			a.warn(cmd, "gt.repository still points to %s; update it with gt repo set-default %s", t.Repo, fullName)
		} else {
			a.done(cmd, "Updated the default repository of this clone to %s", fullName)
		}
	}
}

// movedRemoteURL returns raw, a Git remote URL of repository old, for repository fullName instead,
// keeping its protocol, host and .git suffix.
func movedRemoteURL(raw, old, fullName string) (string, bool) {
	base, suffix := strings.TrimSuffix(raw, "/"), ""
	if trimmed, ok := strings.CutSuffix(base, ".git"); ok {
		base, suffix = trimmed, ".git"
	}
	at := len(base) - len(old)
	if at < 1 || !strings.EqualFold(base[at:], old) || !strings.ContainsRune("/:", rune(base[at-1])) {
		return "", false
	}
	return base[:at] + fullName + suffix, true
}

func (a *application) repoTransferCommand() *cobra.Command {
	var teams []string
	command := &cobra.Command{
		Use:   "transfer <new-owner>",
		Short: "Transfer a repository to another owner",
		Long: `Transfer a repository to another user or organization. --team grants teams of a new owning
organization access to it.

Gitea completes the transfer at once when you may create repositories for the new owner, such as
in an organization you can create repositories in. Otherwise the transfer waits until the new owner
accepts it on the repository's page, and the repository keeps its owner until then.`,
		Example: `# Transfer the repository of the current clone to an organization
$ gt repo transfer acme --team developers

# Offer another repository to a user
$ gt repo transfer bob -R alice/project`,
		Annotations: map[string]string{"gt.confirm": "true"},
		Args:        nonBlankArgs(cobra.ExactArgs(1), "new owner"),
		RunE: func(cmd *cobra.Command, args []string) error {
			newOwner := strings.TrimSpace(args[0])
			if cmd.Flags().Changed("team") && len(teams) == 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--team requires a team name"))
			}
			if err := requireNames("team", "", teams); err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if owner, _ := t.ownerName(); strings.EqualFold(owner, newOwner) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is already owned by %s", t.Repo, owner))
			}
			inferred, err := a.inferredRepository(t, "transfer", false)
			if err != nil {
				return err
			}
			var teamIDs []int64
			if len(teams) > 0 {
				byName, err := (services.Teams{Client: t.client}).ByName(cmd.Context(), newOwner)
				if services.IsNotFound(err) {
					if missing := services.OwnerExists(cmd.Context(), t.client, newOwner); missing != nil {
						return missing
					}
					return fmt.Errorf("--team requires an organization as the new owner: %w", err)
				}
				if err != nil {
					return err
				}
				for _, team := range teams {
					match, ok := byName[strings.ToLower(team)]
					if !ok {
						return services.NotFound("team", strconv.Quote(team), "organization "+newOwner)
					}
					teamIDs = append(teamIDs, match.ID)
				}
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Transfer " + t.Repo + inferred + " to " + newOwner + "?", NonTTY: "transfer " + t.Repo}); err != nil {
				return err
			}
			result, err := (services.Repositories{Client: t.client}).Transfer(cmd.Context(), t.Repo, newOwner, teamIDs)
			if err != nil {
				return err
			}
			if result.Pending {
				a.done(cmd, "Requested the transfer of %s to %s; it completes when %s accepts it", t.Repo, newOwner, newOwner)
			} else {
				a.done(cmd, "Transferred repository %s to %s", t.Repo, result.FullName)
				a.repositoryMoved(cmd, t, result.FullName)
			}
			return a.jsonResult(cmd, result)
		},
	}
	command.Flags().StringSliceVar(&teams, "team", nil, "Grant this team of the new owning organization access (repeatable)")
	command.ValidArgsFunction = firstArg(a.completeOrgs())
	return jsonOutput[services.RepositoryTransfer](command)
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
		Args:        nonBlankArgs(cobra.MaximumNArgs(1), "repository"),
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
	return a.dryRunnable(command)
}
