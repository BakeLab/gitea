// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"github.com/spf13/cobra"
)

func (t target) hosts() map[string]settings.Host {
	return map[string]settings.Host{t.Host: t.host}
}

// ownsRemote reports whether the Git remote URL raw names the target's instance and repository.
func (t target) ownsRemote(raw string) bool {
	host, repo, err := settings.RepositoryURL(raw, t.hosts())
	return err == nil && host == t.Host && strings.EqualFold(repo, t.Repo)
}

// remoteCredentials returns Git transport arguments for the URL of the named remote.
func (a *application) remoteCredentials(ctx context.Context, t target, remote string) ([]string, error) {
	raw, err := a.git(ctx, "remote", "get-url", remote)
	if err != nil {
		return nil, err
	}
	return a.gitTransportArguments(t, settings.RemoteIsSSH(raw))
}

func (a *application) checkoutRemote(ctx context.Context, target target, snapshot settings.Snapshot) (string, error) {
	explicit := a.options.remote
	if explicit == "" {
		explicit = snapshot.Preferences.Remote
	}
	if explicit != "" {
		raw, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", explicit)
		if err == nil && target.ownsRemote(raw) {
			return explicit, nil
		}
		return "", errors.New("selected remote does not match the resolved instance and repository")
	}
	matches, err := a.remotesForRepositoryURLs(ctx, target.hosts(), "", "", target.Repo)
	if err != nil {
		return "", err
	}
	preferredRemotes := []string{"upstream", "origin"}
	if _, tracking := git.CurrentUpstreamRemote(ctx, a.gitProbe(), a.options.cwd); tracking != "" {
		preferredRemotes = append([]string{tracking}, preferredRemotes...)
	}
	for _, preferred := range preferredRemotes {
		if slices.Contains(matches, preferred) {
			return preferred, nil
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", errNoMatchingRemote
	}
	return "", errors.New("multiple Git remotes match the selected repository; select --remote")
}

var errNoMatchingRemote = errors.New("no Git remote matches the selected repository; add its base remote before checkout")

func (a *application) pullCheckoutCommand() *cobra.Command {
	var branch string
	var detached, update bool
	cmd := &cobra.Command{Use: "checkout <number>", Short: "Check out a pull request in a matching branch or detached HEAD", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		if detached && (cmd.Flags().Changed("branch") || update) {
			return clierr.Wrap(clierr.Usage, errors.New("--detach and --branch/--update are mutually exclusive"))
		}
		if cmd.Flags().Changed("branch") && strings.TrimSpace(branch) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be empty"))
		}
		if branch == "" && !detached {
			branch = fmt.Sprintf("pr/%d", n)
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		remote, err := a.checkoutRemote(cmd.Context(), target, snapshot)
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		pull, err := (services.Pulls{Client: target.client, Repository: target.Repo}).View(cmd.Context(), n)
		if err != nil {
			return err
		}
		if pull.HeadSHA == "" {
			return errors.New("Gitea did not return the PR head commit")
		}
		rawRemote, err := a.git(cmd.Context(), "remote", "get-url", remote)
		if err != nil {
			return err
		}
		if !target.ownsRemote(rawRemote) {
			return clierr.Wrap(clierr.Usage, errors.New("selected Git remote changed; resolve the repository again before checkout"))
		}
		credentialRemote := rawRemote
		options := git.CheckoutOptions{
			Detached: detached, Update: update, Directory: a.options.cwd, Remote: remote, Number: n, Branch: branch,
			Prompt: a.policy(snapshot).CanPrompt(a.deps.Streams), ExpectedCommit: pull.HeadSHA,
		}
		undoRemote := func() {}
		if !pull.HeadRemoteDeleted && pull.Head != "" && !detached {
			headRemote, headURL, undo, ensureErr := a.ensureHeadRemote(cmd.Context(), target, pull)
			if ensureErr != nil {
				return ensureErr
			}
			if headRemote != "" {
				options.HeadRemote, options.HeadBranch = headRemote, pull.Head
				credentialRemote, undoRemote = headURL, undo
			}
		}
		credentialArgs, err := a.gitTransportArguments(target, settings.RemoteIsSSH(credentialRemote))
		if err != nil {
			undoRemote()
			return err
		}
		options.CredentialArgs = credentialArgs
		result, err := git.CheckoutPull(cmd.Context(), a.deps.Git, options)
		if err != nil {
			undoRemote()
			return err
		}
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		if result.Detached {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Checked out %s in detached HEAD\n", result.Commit)
			return err
		}
		if result.Tracking != "" {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Checked out %s (%s) tracking %s\n", result.Branch, result.Commit, result.Tracking)
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Checked out %s (%s)\n", result.Branch, result.Commit)
		return err
	}}
	cmd.Flags().BoolVar(&update, "update", false, "Fast-forward an existing branch to the PR head; reject divergence")
	cmd.Flags().BoolVar(&detached, "detach", false, "Check out the PR commit without creating or changing a branch")
	cmd.Flags().StringVarP(&branch, "branch", "b", "", "Local branch name (default: pr/NUMBER; existing branch must match PR head)")
	return cmd
}

// ensureHeadRemote returns the remote holding the PR head, adding one when none matches.
// An empty name means the head repository has no usable clone URL and the base remote's PR ref should be used.
func (a *application) ensureHeadRemote(ctx context.Context, target target, pull services.Pull) (name, remoteURL string, undo func(), err error) {
	matches, err := a.remotesForRepositoryURLs(ctx, target.hosts(), pull.HeadCloneURL, pull.HeadSSHURL, pull.HeadRepo)
	if err != nil {
		return "", "", nil, err
	}
	existing := ""
	switch {
	case a.options.remote != "" && slices.Contains(matches, a.options.remote):
		existing = a.options.remote
	case len(matches) == 1:
		existing = matches[0]
	case len(matches) > 1:
		for _, preferred := range []string{"origin", "upstream"} {
			if slices.Contains(matches, preferred) {
				existing = preferred
				break
			}
		}
		if existing == "" {
			return "", "", nil, clierr.Wrap(clierr.Usage, errors.New("multiple Git remotes match the pull request head repository; select --remote"))
		}
	}
	if existing != "" {
		raw, err := a.git(ctx, "remote", "get-url", existing)
		return existing, raw, func() {}, err
	}
	remoteURL, _, urlErr := cloneURLForProtocol(pull.HeadCloneURL, pull.HeadSSHURL, target.gitProtocol)
	if urlErr != nil {
		return "", "", nil, nil
	}
	name = "pulls/" + pull.HeadRepo
	if pull.HeadRepo == "" {
		name = "pulls/head"
	}
	if _, err := a.git(ctx, "remote", "add", "--", name, remoteURL); err != nil {
		return "", "", nil, fmt.Errorf("could not add head remote %s: %w", name, err)
	}
	// Detached from the command context so an interrupt (Ctrl-C) still removes the half-used remote.
	cleanup := context.WithoutCancel(ctx)
	return name, remoteURL, func() { _, _ = a.git(cleanup, "remote", "remove", "--", name) }, nil
}
