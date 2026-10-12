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
	"github.com/spf13/cobra"
)

func (a *application) pullCleanCommand() *cobra.Command {
	var ignoreSHA bool
	cmd := &cobra.Command{Use: "clean <number>", Short: "Delete local and remote feature branches for a closed pull request", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		snapshot, err := a.deps.Settings.Load()
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
		if pull.Head == "" {
			return errors.New("Gitea did not return the pull request head branch")
		}
		if !ignoreSHA && pull.HeadSHA == "" {
			return errors.New("Gitea did not return the pull request head commit; retry with --ignore-sha")
		}
		matchingRemotes, err := a.remotesForRepositoryURLs(cmd.Context(), target.hosts(), pull.HeadCloneURL, pull.HeadSSHURL, pull.HeadRepo)
		if err != nil {
			return err
		}
		localBranch, remote, err := git.FindCleanBranch(cmd.Context(), a.deps.Git, git.FindCleanBranchOptions{
			Directory: a.options.cwd, HeadSHA: pull.HeadSHA, HeadBranch: pull.Head, IgnoreSHA: ignoreSHA, DefaultBranch: repository.DefaultBranch,
			PreferredNames: []string{fmt.Sprintf("pr/%d", n)}, MatchingRemotes: matchingRemotes,
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
		description := fmt.Sprintf("Delete local branch %q for pull request %s#%d", localBranch, target.Repo, n)
		if ignoreSHA {
			description += " (matched by name; local commits not in the pull request are lost)"
		}
		if deleteRemote {
			description += fmt.Sprintf(" and remote branch %q", remote+"/"+pull.Head)
		}
		if err := a.confirm(cmd, description); err != nil {
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
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted local branch %s\n", result.LocalBranch)
		if err != nil {
			return err
		}
		if result.RemoteDeleted {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted remote branch %s/%s\n", result.Remote, result.RemoteBranch)
			return err
		}
		if result.RemoteSkipped {
			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", result.RemoteSkipReason)
			return err
		}
		return nil
	}}
	cmd.Flags().BoolVar(&ignoreSHA, "ignore-sha", false, "Find the local branch by name instead of commit hash")
	return cmd
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
