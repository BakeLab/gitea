// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"

	"github.com/spf13/cobra"
)

func (a *application) repositorySyncCommand() *cobra.Command {
	var branch string
	command := &cobra.Command{
		Use:   "sync [<repository>]",
		Short: "Sync a branch with its remote or its fork's parent",
		Long: `Fast-forward a local branch (default: the repository's default branch) to the same branch on
the repository's Git remote, without switching branches. Diverged branches are left unchanged.

Given a repository without a Git remote here, sync updates it on the server instead: a fork
fast-forwards to its parent, and a pull mirror fetches its source. Gitea updates a mirror as a
whole and in the background, so --branch is refused for a mirror, and the JSON result names
only the source, as remote.`,
		Example: `# Fast-forward the default branch from the repository's remote
$ gt repo sync

# Fast-forward another branch
$ gt repo sync --branch release/1.0

# Update a fork on the server from its parent
$ gt repo sync bob/project`,
		Annotations: map[string]string{"gt.arguments": repositoryArguments},
		Args:        nonBlankArgs(cobra.MaximumNArgs(1), "repository"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("branch") && strings.TrimSpace(branch) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be empty"))
			}
			if branch != "" {
				if err := validateBranch(branch); err != nil {
					return err
				}
			}
			if err := a.repositoryArgument(args); err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			inside := a.insideClone(cmd.Context())
			remote, err := "", errNoMatchingRemote
			if inside {
				remote, err = a.checkoutRemote(cmd.Context(), target, snapshot)
			}
			switch {
			case errors.Is(err, errNoMatchingRemote) && len(args) > 0:
				return a.syncOnServer(cmd, target, branch)
			case !inside:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("repo sync requires a Git clone of %s; run it inside one, or pass a fork to update it on the server: gt repo sync OWNER/REPO", target.Repo))
			case errors.Is(err, errNoMatchingRemote):
				return clierr.Wrap(clierr.Usage, errors.New("no Git remote of this clone points to "+target.Repo+"; add one with git remote add, or pass --remote"))
			case err != nil:
				return clierr.Wrap(clierr.Usage, err)
			}
			if branch == "" {
				repository, err := (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo)
				if err != nil {
					return err
				}
				branch = repository.DefaultBranch
			}
			raw, err := a.git(cmd.Context(), "remote", "get-url", remote)
			if err != nil {
				return err
			}
			if !target.ownsRemote(raw) {
				return clierr.Wrap(clierr.Usage, errors.New("selected Git remote changed; resolve the repository again before sync"))
			}
			credentials, err := a.gitTransportArguments(target, settings.RemoteIsSSH(raw))
			if err != nil {
				return err
			}
			result, err := git.SyncBranch(cmd.Context(), a.deps.Git, git.SyncOptions{Directory: a.options.cwd, Remote: remote, Branch: branch, CredentialArgs: credentials, Prompt: a.policy(snapshot).CanPrompt(a.deps.Streams)})
			if err != nil {
				return err
			}
			a.reportSync(cmd, result, remote+"/"+result.Branch)
			return a.jsonResult(cmd, result)
		},
	}
	command.Flags().StringVarP(&branch, "branch", "b", "", "Branch to sync (default: the repository's default branch)")
	_ = command.RegisterFlagCompletionFunc("branch", a.completeArgumentBranches())
	command.ValidArgsFunction = a.completeRepos()
	return jsonOutput[git.SyncResult](command)
}

// syncOnServer updates the repository of t on the server: a fork's branch, the default branch
// unless named, fast-forwards to its parent's, and a pull mirror fetches its source.
func (a *application) syncOnServer(cmd *cobra.Command, t target, branch string) error {
	repositories := services.Repositories{Client: t.client}
	item, err := repositories.View(cmd.Context(), t.Repo)
	if err != nil {
		return err
	}
	switch {
	case item.Mirror:
		if cmd.Flags().Changed("branch") {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--branch cannot be used for the mirror %s: Gitea updates a mirror as a whole", item.FullName))
		}
		if err := repositories.MirrorSync(cmd.Context(), item.FullName); err != nil {
			return err
		}
		a.done(cmd, "Started updating the mirror %s from its source", item.FullName)
		return a.jsonResult(cmd, git.SyncResult{Remote: mirrorSource(item)})
	case !item.Fork || item.Parent == nil:
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s is neither a fork nor a mirror, and has no Git remote here; run gt repo sync inside a clone of it", item.FullName))
	}
	branch = cmp.Or(branch, item.DefaultBranch)
	branches := services.Branches{Client: t.client}
	before, err := branches.View(cmd.Context(), item.FullName, branch)
	if err != nil {
		return err
	}
	if _, err := repositories.MergeUpstream(cmd.Context(), item.FullName, branch); err != nil {
		return err
	}
	after, err := branches.View(cmd.Context(), item.FullName, branch)
	if err != nil {
		return err
	}
	result := git.SyncResult{Branch: branch, Commit: after.SHA, Previous: before.SHA, Remote: item.Parent.FullName}
	if result.Previous == result.Commit {
		a.done(cmd, "%s %s is already up to date with %s", item.FullName, branch, item.Parent.FullName)
	} else {
		a.done(cmd, "Synced %s %s from %s (fast-forward %s..%s)", item.FullName, branch, item.Parent.FullName, shortSHA(result.Previous), shortSHA(result.Commit))
	}
	return a.jsonResult(cmd, result)
}

// mirrorSource names the source of a pull mirror for the sync result: its URL without
// credentials, or the path of a source on the server's disk, which carries none.
func mirrorSource(item services.Repository) string {
	if source := mirrorSourceURL(item); source != "" {
		return source
	}
	if strings.HasPrefix(item.OriginalURL, "/") && !strings.ContainsAny(item.OriginalURL, "@ \t\r\n") {
		return item.OriginalURL
	}
	return ""
}

// reportSync tells how a sync from source, such as origin/main, changed the branch.
func (a *application) reportSync(cmd *cobra.Command, result git.SyncResult, source string) {
	switch result.Previous {
	case result.Commit:
		a.done(cmd, "%s is already up to date with %s", result.Branch, source)
	case "":
		a.done(cmd, "Created %s from %s (%s)", result.Branch, source, shortSHA(result.Commit))
	default:
		a.done(cmd, "Synced %s from %s (fast-forward %s..%s)", result.Branch, source, shortSHA(result.Previous), shortSHA(result.Commit))
	}
}
