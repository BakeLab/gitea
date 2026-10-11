// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/settings"
	"github.com/spf13/cobra"
)

func (a *application) repositorySyncCommand() *cobra.Command {
	var branch string
	command := &cobra.Command{Use: "sync [repository]", Short: "Fast-forward a local branch from the selected repository remote", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("branch") && strings.TrimSpace(branch) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be empty"))
		}
		if err := a.repositoryArgument(args); err != nil {
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
		remote, err := a.checkoutRemote(cmd.Context(), target, snapshot)
		if err != nil {
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
		if a.options.json != "" {
			return a.writeJSON(cmd, result)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Synced %s (%s) from %s\n", result.Branch, result.Commit, result.Remote)
		return err
	}}
	command.Flags().StringVarP(&branch, "branch", "b", "", "Local and remote branch (default: repository default branch)")
	return command
}
