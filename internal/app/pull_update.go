// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"

	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) pullUpdateBranchCommand() *cobra.Command {
	var rebase bool
	cmd := &cobra.Command{Use: "update-branch [<number> | <url> | <branch>]", Short: "Update the head branch of a pull request with its base branch", Long: `Bring the new commits of the base branch into the head branch of a pull request on Gitea,
with a merge commit, or with --rebase by rebasing the head branch onto the base branch, which
rewrites it.

Only the branch on Gitea changes; update a local branch of the pull request afterwards with
git pull, or after --rebase with git pull --rebase. Gitea cannot update the head of an AGit
pull request; push to it instead.`, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		service := services.Pulls{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n)
		if err != nil {
			return err
		}
		ref := refLabel(t.Repo, n, item.Title)
		switch {
		case item.State != "open":
			return fmt.Errorf("pull request %s is %s; only an open pull request can be updated", ref, pullState(item))
		case item.Head == "":
			return fmt.Errorf("pull request %s is an AGit pull request, whose head Gitea cannot update; push to it instead", ref)
		case item.HeadRemoteDeleted:
			return fmt.Errorf("pull request %s cannot be updated: its head branch %s was deleted", ref, item.Head)
		}
		switch err := service.UpdateBranch(cmd.Context(), item, rebase); {
		case errors.Is(err, services.ErrPullUpToDate):
			a.warn(cmd, "Pull request %s is already up to date with %s", ref, item.Base)
			return a.pullResult(cmd, t, item)
		case errors.Is(err, services.ErrRebaseUpdateDisabled):
			return fmt.Errorf("rebase updates are disabled in %s; update with a merge commit (without --rebase)", t.Repo)
		case errors.Is(err, services.ErrPullUpdateConflicts):
			return explain(err, "cannot update pull request %s: its head branch %s conflicts with %s; merge or rebase it locally and push it", ref, item.Head, item.Base)
		case err != nil:
			return err
		}
		updated, err := service.View(cmd.Context(), n)
		if err != nil {
			return fmt.Errorf("pull request %s was updated, but reading it failed: %w", ref, err)
		}
		if rebase {
			a.done(cmd, "Rebased pull request %s onto %s", ref, item.Base)
		} else {
			a.done(cmd, "Merged %s into pull request %s", item.Base, ref)
		}
		return a.pullResult(cmd, t, updated)
	}}
	cmd.Flags().BoolVar(&rebase, "rebase", false, "Rebase the head branch onto the base branch instead of merging the base branch into it")
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	cmd.Example = `$ gt pr update-branch
$ gt pr update-branch 13 --rebase`
	return cmd
}
