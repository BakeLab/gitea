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
	host, repo, err := settings.RemoteRepository(raw, t.hosts())
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
	if explicit := a.options.remote; explicit != "" {
		raw, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote", "get-url", explicit)
		if err != nil {
			return "", clierr.Wrap(clierr.Usage, fmt.Errorf("Git remote %q does not exist", explicit))
		}
		if target.ownsRemote(raw) {
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
	if preference := snapshot.Preferences.Remote; preference != "" {
		preferredRemotes = append([]string{preference}, preferredRemotes...)
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
	cmd := &cobra.Command{Use: "checkout {<number> | <url> | <branch>}", Aliases: []string{"co"}, Short: "Check out a pull request", Long: `Check out the head of a pull request in a local branch that tracks the head branch, so
that pr commands without an argument select the pull request, and git push updates it when the
branch is named like the head branch. gt prints the push command for a branch named otherwise.`, Annotations: pullArgumentRequiredAnnotation(nil), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		if detached && (cmd.Flags().Changed("branch") || update) {
			return clierr.Wrap(clierr.Usage, errors.New("--detach cannot be combined with --branch or --update"))
		}
		if cmd.Flags().Changed("branch") && strings.TrimSpace(branch) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be empty"))
		}
		if branch != "" {
			if err := validateBranch(branch); err != nil {
				return err
			}
		}
		if !a.insideClone(cmd.Context()) {
			return clierr.Wrap(clierr.Usage, errors.New("pr checkout must run inside a Git clone"))
		}
		target, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		snapshot, err := a.settings()
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
		if branch == "" && !detached {
			branch = a.checkoutBranchName(cmd.Context(), target, pull)
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
		if result.Detached {
			a.done(cmd, "Checked out #%d at %s (detached HEAD)", n, shortSHA(result.Commit))
		} else if options.HeadBranch != "" && result.Branch != options.HeadBranch {
			a.done(cmd, "Checked out #%d into %s; push to it with: git push %s HEAD:%s", n, result.Branch, options.HeadRemote, options.HeadBranch)
		} else {
			a.done(cmd, "Checked out #%d into %s", n, result.Branch)
		}
		return a.jsonResult(cmd, result)
	}}
	cmd.Flags().BoolVar(&update, "update", false, "Fast-forward an existing branch to the PR head; reject divergence")
	cmd.Flags().BoolVar(&detached, "detach", false, "Check out the PR commit without creating or changing a branch")
	cmd.Flags().StringVarP(&branch, "branch", "b", "", "Local branch name (default: the head branch name when free, else OWNER/BRANCH or pr/NUMBER)")
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `$ gt pr checkout 13
$ gt pr co feature/login
$ gt pr checkout 13 --update`
	return jsonOutput(cmd, jsonFields[git.CheckoutResult]())
}

// checkoutBranchName names the local branch for a pull request: its head branch name when
// that is free or already at the head, else OWNER/BRANCH for a fork, else pr/NUMBER. A branch that
// an earlier checkout of the same pull request created, or that tracks the head branch, such as the
// author's own, is reused, so that --update can advance it. A fork's head name is not reused when
// that local branch tracks something else, such as main.
func (a *application) checkoutBranchName(ctx context.Context, t target, pull services.Pull) string {
	fallback := fmt.Sprintf("pr/%d", pull.Number)
	if pull.HeadRemoteDeleted || pull.Head == "" {
		return fallback
	}
	var candidates []string
	if !strings.HasPrefix(pull.Head, "-") { // a valid remote branch name, but not a local one
		candidates = append(candidates, pull.Head)
	}
	if owner, _, _ := strings.Cut(pull.HeadRepo, "/"); owner != "" && !strings.EqualFold(pull.HeadRepo, t.Repo) {
		candidates = append(candidates, owner+"/"+pull.Head)
	}
	sameRepo := strings.EqualFold(pull.HeadRepo, t.Repo)
	for _, name := range candidates {
		tip, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+name+"^{commit}")
		if err != nil {
			if !a.branchPathTaken(ctx, name) {
				return name
			}
			continue
		}
		config := git.ReadBranchConfig(ctx, a.gitProbe(), a.options.cwd, name)
		tracksHead := config.Merge == pull.Head && pull.HeadRepo != "" && strings.EqualFold(a.remoteRepository(ctx, t, config.Remote), pull.HeadRepo)
		if config.PullNumber == pull.Number || tracksHead || (tip == pull.HeadSHA && (sameRepo || config.Remote == "")) {
			return name
		}
	}
	return fallback
}

// branchPathTaken reports whether a branch name cannot be created because another branch is a
// directory prefix of it (feature for feature/login) or lives below it.
func (a *application) branchPathTaken(ctx context.Context, name string) bool {
	for i, r := range name {
		if r != '/' {
			continue
		}
		if _, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+name[:i]+"^{commit}"); err == nil {
			return true
		}
	}
	below, err := a.gitProbe().Output(ctx, a.options.cwd, false, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/"+name+"/")
	return err == nil && below != ""
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
	if _, err := a.gitProbe().Output(ctx, a.options.cwd, false, "check-ref-format", "refs/remotes/"+name+"/head"); err != nil {
		return "", "", nil, nil // such as for a repository named x.lock, which Git refuses as a remote name
	}
	if _, err := a.git(ctx, "remote", "add", "--", name, remoteURL); err != nil {
		return "", "", nil, fmt.Errorf("could not add head remote %s: %w", name, err)
	}
	// Detached from the command context so an interrupt (Ctrl-C) still removes the half-used remote.
	cleanup := context.WithoutCancel(ctx)
	return name, remoteURL, func() { _, _ = a.git(cleanup, "remote", "remove", "--", name) }, nil
}
