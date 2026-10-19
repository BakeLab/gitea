// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullCleanCommand() *cobra.Command {
	var ignoreSHA bool
	cmd := &cobra.Command{Use: "clean [<number> | <url> | <branch>]", Short: "Delete the branches of a merged or closed pull request", Annotations: pullArgumentsAnnotation(map[string]string{"gt.confirm": "true"}), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		target, n, err := a.pullSelectorAny(cmd, args)
		if err != nil {
			return err
		}
		snapshot, err := a.settings()
		if err != nil {
			return err
		}
		repository, err := (services.Repositories{Client: target.client}).View(cmd.Context(), target.Repo)
		if err != nil {
			return err
		}
		if repository.DefaultBranch == "" {
			return clierr.Wrap(clierr.Usage, errors.New("repository has no default branch; cannot clean while checked out on the feature branch"))
		}
		pull, err := (services.Pulls{Client: target.client, Repository: target.Repo}).View(cmd.Context(), n)
		if err != nil {
			return err
		}
		if pull.State == "open" {
			return errors.New("pull request is still open; close or merge it before cleaning branches")
		}
		// AGit pull requests have no head branch; their local branch is found by its commit.
		if pull.Head == "" && ignoreSHA {
			return errors.New("Gitea did not return the pull request head branch; clean it without --ignore-sha")
		}
		if !ignoreSHA && pull.HeadSHA == "" {
			return errors.New("Gitea did not return the pull request head commit; retry with --ignore-sha")
		}
		matchingRemotes, err := a.remotesForRepositoryURLs(cmd.Context(), target.hosts(), pull.HeadCloneURL, pull.HeadSSHURL, pull.HeadRepo)
		if err != nil {
			return err
		}
		// pr checkout names a fork's branch after its head, or OWNER/BRANCH when the head name is taken.
		preferred := []string{fmt.Sprintf("pr/%d", n)}
		if owner, _, _ := strings.Cut(pull.HeadRepo, "/"); owner != "" && !strings.EqualFold(pull.HeadRepo, target.Repo) {
			preferred = append(preferred, owner+"/"+pull.Head)
		}
		localBranch, remote, err := git.FindCleanBranch(cmd.Context(), a.deps.Git, git.FindCleanBranchOptions{
			Directory: a.options.cwd, HeadSHA: pull.HeadSHA, HeadBranch: pull.Head, IgnoreSHA: ignoreSHA, DefaultBranch: repository.DefaultBranch,
			PreferredNames: preferred, MatchingRemotes: matchingRemotes, PullNumber: n,
		})
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		deleteRemote := !pull.HeadRemoteDeleted && pull.HeadCanPush
		if deleteRemote && remote == "" {
			switch len(matchingRemotes) {
			case 0:
				return clierr.Wrap(clierr.Usage, errors.New("no Git remote matches the pull request head repository; add it before deleting the remote branch"))
			case 1:
				remote = matchingRemotes[0]
			default:
				return clierr.Wrap(clierr.Usage, errors.New("multiple Git remotes match the pull request head repository; select --remote"))
			}
		}
		if a.options.remote != "" {
			if deleteRemote && remote != "" && remote != a.options.remote {
				return clierr.Wrap(clierr.Usage, errors.New("selected --remote does not match the pull request head remote"))
			}
			if remote == "" {
				remote = a.options.remote
			}
		}
		var credentialArgs []string
		if deleteRemote {
			if credentialArgs, err = a.remoteCredentials(cmd.Context(), target, remote); err != nil {
				return err
			}
		}
		description := fmt.Sprintf("Delete local branch %s of %s", localBranch, refLabel(target.Repo, n, pull.Title))
		if deleteRemote {
			description += fmt.Sprintf(" and remote branch %s/%s", remote, pull.Head)
		}
		prompt := description + "?"
		if ignoreSHA {
			prompt += " Local commits that are not in the pull request are lost."
		}
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: prompt, NonTTY: strings.ToLower(description[:1]) + description[1:]}); err != nil {
			return err
		}
		result, err := git.CleanPull(cmd.Context(), a.deps.Git, git.CleanOptions{
			Directory: a.options.cwd, DefaultBranch: repository.DefaultBranch, LocalBranch: localBranch,
			Remote: remote, RemoteBranch: pull.Head, CredentialArgs: credentialArgs,
			Prompt: a.policy(snapshot).CanPrompt(a.deps.Streams), DeleteRemote: deleteRemote,
		})
		if err != nil {
			return err
		}
		a.done(cmd, "Deleted local branch %s", result.LocalBranch)
		switch {
		case result.RemoteDeleted:
			a.done(cmd, "Deleted remote branch %s/%s", result.Remote, result.RemoteBranch)
		case pull.HeadRemoteDeleted:
			a.warn(cmd, "Remote branch %s was already deleted", pull.Head)
		case !pull.HeadCanPush:
			a.warn(cmd, "Kept remote branch %s: you cannot push to %s", pull.Head, pull.HeadRepo)
		}
		return a.jsonResult(cmd, result)
	}}
	cmd.Flags().BoolVar(&ignoreSHA, "ignore-sha", false, "Find the local branch by name instead of commit hash")
	cmd.ValidArgsFunction = firstArg(a.completePulls("closed"))
	cmd.Example = `# After #13 was merged, delete its branch locally and on Gitea
$ gt pr clean 13`
	return jsonOutput(cmd, jsonFields[git.CleanResult]())
}

func (a *application) remotesForRepositoryURLs(ctx context.Context, hosts map[string]settings.Host, cloneURL, sshURL, fullName string) ([]string, error) {
	names, err := a.git(ctx, "remote")
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	addWanted := func(host, repo string) {
		wanted[host+"\t"+strings.ToLower(repo)] = true
	}
	for _, raw := range []string{cloneURL, sshURL} {
		if raw == "" {
			continue
		}
		host, repo, err := settings.RepositoryURL(raw, hosts)
		if err != nil {
			continue
		}
		addWanted(host, repo)
	}
	if fullName != "" {
		for host := range hosts {
			addWanted(host, fullName)
		}
	}
	matches := []string{}
	for name := range strings.FieldsSeq(names) {
		raw, err := a.git(ctx, "remote", "get-url", name)
		if err != nil {
			return nil, err
		}
		host, repo, err := settings.RepositoryURL(raw, hosts)
		if err != nil {
			continue
		}
		if wanted[host+"\t"+strings.ToLower(repo)] {
			matches = append(matches, name)
		}
	}
	return matches, nil
}
