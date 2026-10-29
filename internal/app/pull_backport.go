// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// backportOutput is a backport branch and the pull request opened from it.
type backportOutput struct {
	git.BackportResult
	Pull services.Pull `json:"pull"`
}

func (a *application) pullBackportCommand() *cobra.Command {
	var targets []string
	var branch, milestone string
	var keep, resume bool
	cmd := &cobra.Command{Use: "backport [<number> | <url> | <branch>] --to <branch>", Short: "Backport a merged pull request to other branches", Long: `Cherry-pick the commits a merged pull request left on its base branch onto a new branch for
each --to branch, push it and open a pull request into that branch, titled like the original
with its number appended, with its labels.

A merge commit backports the commits it merged, a rebase or fast-forward merge its commits, and a
squash merge the squashed commit; cherry-picks use -x. The new branch is backport-NUMBER-BRANCH,
started from the --to branch of the base repository's remote, and gt switches back to the branch
it ran on afterwards. Like gt pr create, it pushes to your fork when you cannot push to the
repository; a fork it has to create first is offered only on a terminal, so elsewhere run gt repo
fork --add-remote first. Pushing asks for a confirmation; without a terminal, pass --yes.

A cherry-pick that conflicts is undone, and other --to branches are still tried, as they are
after a --to branch that already has the pull request's changes. With --keep, gt
stops on the backport branch instead: resolve the conflicts, git add them and run git cherry-pick
--continue, then run gt pr backport --continue on that branch to push it and open the pull
request.`, Annotations: pullArgumentsAnnotation(map[string]string{"gt.confirm": "true"}), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if resume {
			if len(args) > 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--continue cannot be combined with a pull request"))
			}
			if err := onlyFlagsWith(cmd, "continue", "it finishes the backport of the checked-out branch", "milestone"); err != nil {
				return err
			}
		} else {
			if len(targets) == 0 {
				return clierr.Wrap(clierr.Usage, errors.New("pr backport requires --to"))
			}
			if err := requireNames("branch", "", targets); err != nil {
				return err
			}
			if len(targets) > 1 && cmd.Flags().Changed("branch") {
				return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be combined with more than one --to"))
			}
			if len(targets) > 1 && keep {
				return clierr.Wrap(clierr.Usage, errors.New("--keep cannot be combined with more than one --to"))
			}
			if cmd.Flags().Changed("branch") && strings.TrimSpace(branch) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--branch cannot be empty"))
			}
			if err := validateBranches(targets); err != nil {
				return err
			}
			if branch != "" {
				if err := validateBranch(branch); err != nil {
					return err
				}
			}
		}
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil && id < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--milestone must be a milestone title or a positive ID"))
		}
		if !a.insideClone(cmd.Context()) {
			return clierr.Wrap(clierr.Usage, errors.New("pr backport requires a Git clone"))
		}
		// The backport branch is pushed after a confirmation, which fails only once it exists.
		if !a.options.yes && !a.canPrompt() {
			return a.confirmAction(cmd, terminal.Confirmation{NonTTY: "push the backport branch"})
		}
		if resume {
			return a.continueBackport(cmd, milestone)
		}
		return a.runBackport(cmd, args, targets, branch, milestone, keep)
	}}
	cmd.Flags().StringArrayVar(&targets, "to", nil, "Branch to backport to (repeatable)")
	cmd.Flags().StringVarP(&branch, "branch", "b", "", "Local branch to cherry-pick onto (default: backport-NUMBER-BRANCH)")
	cmd.Flags().StringVarP(&milestone, "milestone", "m", "", "Milestone title or ID of the backport pull request")
	cmd.Flags().BoolVar(&keep, "keep", false, "Stop at a conflict to resolve it instead of undoing the backport")
	cmd.Flags().BoolVar(&resume, "continue", false, "Push the checked-out backport branch after resolving its conflicts and open its pull request")
	cmd.ValidArgsFunction = firstArg(a.completePulls("closed"))
	_ = cmd.RegisterFlagCompletionFunc("to", a.completeBranches())
	_ = cmd.RegisterFlagCompletionFunc("milestone", a.completeMilestones())
	cmd.Example = `$ gt pr backport 42 --to release/v1.2
$ gt pr backport 42 --to release/v1.2 --to release/v1.1 --yes

# Resolve conflicts yourself
$ gt pr backport 42 --to release/v1.1 --keep
$ git add . && git cherry-pick --continue
$ gt pr backport --continue`
	return jsonOutput(cmd, jsonFields[backportOutput]())
}

func (a *application) runBackport(cmd *cobra.Command, args, targets []string, branch, milestone string, keep bool) error {
	ctx := cmd.Context()
	t, n, err := a.pullSelectorAny(cmd, args)
	if err != nil {
		return err
	}
	snapshot, err := a.settings()
	if err != nil {
		return err
	}
	remote, err := a.checkoutRemote(ctx, t, snapshot)
	if err != nil {
		return clierr.Wrap(clierr.Usage, err)
	}
	pull, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(ctx, n)
	if err != nil {
		return err
	}
	if !pull.Merged || pull.MergeCommitSHA == nil || *pull.MergeCommitSHA == "" {
		return fmt.Errorf("pull request #%d is not merged; only merged pull requests can be backported", n)
	}
	if err := (services.Branches{Client: t.client}).RequireAll(ctx, t.Repo, targets); err != nil {
		if services.IsNotFound(err) {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--to requires an existing branch: %w", err))
		}
		return err
	}
	milestoneID, err := backportMilestone(cmd, t, milestone)
	if err != nil {
		return err
	}
	credentials, err := a.remoteCredentials(ctx, t, remote)
	if err != nil {
		return err
	}
	var outputs []backportOutput
	var failures []error
	for _, to := range targets {
		name := branch
		if name == "" {
			name = fmt.Sprintf("backport-%d-%s", n, strings.ReplaceAll(to, "/", "-"))
		}
		// Pushing over a leftover branch of that name would fail, however often it is retried.
		if _, err := a.gitProbe().Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err != nil {
			if _, err := (services.Branches{Client: t.client}).View(ctx, t.Repo, name); err == nil {
				failures = append(failures, fmt.Errorf("branch %s already exists in %s; choose another with --branch", name, t.Repo))
				continue
			}
		}
		result, err := git.Backport(ctx, a.deps.Git, git.BackportOptions{
			CredentialArgs: credentials, Directory: a.options.cwd, Remote: remote, Prompt: a.policy(snapshot).CanPrompt(a.deps.Streams),
			Number: n, Base: pull.Base, MergeCommit: *pull.MergeCommitSHA, MergeBase: pull.MergeBase, Target: to, Branch: name, Keep: keep,
			Hold: a.heldGit,
		})
		conflict, conflicted := errors.AsType[*git.BackportConflict](err)
		if _, skipped := errors.AsType[*git.BackportSkipped](err); skipped || conflicted && !conflict.Kept {
			failures = append(failures, err)
			continue
		}
		if err != nil {
			return errors.Join(append(failures, err, a.reportBackports(cmd, outputs))...)
		}
		output, err := a.openBackportPull(cmd, t, snapshot, pull, result, milestoneID)
		if err != nil {
			return errors.Join(append(failures, err, a.reportBackports(cmd, outputs))...)
		}
		outputs = append(outputs, output)
	}
	return errors.Join(append(failures, a.reportBackports(cmd, outputs))...)
}

func (a *application) continueBackport(cmd *cobra.Command, milestone string) error {
	t, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	milestoneID, err := backportMilestone(cmd, t, milestone)
	if err != nil {
		return err
	}
	result, err := git.ContinueBackport(cmd.Context(), a.deps.Git, a.options.cwd)
	if err != nil {
		return err
	}
	snapshot, err := a.settings()
	if err != nil {
		return err
	}
	pull, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), result.Number)
	if err != nil {
		return err
	}
	output, err := a.openBackportPull(cmd, t, snapshot, pull, result, milestoneID)
	if err != nil {
		return err
	}
	return a.reportBackports(cmd, []backportOutput{output})
}

// backportMilestone returns the ID of the milestone that backport pull requests get, or 0.
func backportMilestone(cmd *cobra.Command, t target, milestone string) (int64, error) {
	if milestone == "" || milestone == "0" {
		return 0, nil
	}
	return milestoneID(cmd, t, milestone)
}

// openBackportPull pushes a backport branch and opens its pull request into the backport's target.
func (a *application) openBackportPull(cmd *cobra.Command, t target, snapshot settings.Snapshot, pull services.Pull, result git.BackportResult, milestone int64) (backportOutput, error) {
	ctx := cmd.Context()
	options := services.PullOptions{Base: result.Target, Title: pull.Title, Body: fmt.Sprintf("Backport #%d by @%s", pull.Number, pull.Author)}
	if suffix := fmt.Sprintf("(#%d)", pull.Number); !strings.HasSuffix(options.Title, suffix) {
		options.Title += " " + suffix
	}
	if body := strings.TrimSpace(pull.Body); body != "" {
		options.Body += "\n\n" + body
	}
	for _, label := range pull.Labels {
		if !label.Archived {
			options.Labels = append(options.Labels, label.ID)
		}
	}
	options.Milestone = milestone
	allowEdits := true
	options.AllowMaintainerEdit = &allowEdits
	head, err := a.ensureHeadPublished(cmd, t, snapshot, result.Branch, &pullBase{branch: result.Target})
	if err != nil {
		return backportOutput{}, fmt.Errorf("backported #%d to branch %s, but could not push it: %w; to retry, switch to it and run gt pr backport --continue", pull.Number, result.Branch, err)
	}
	options.Head = head
	item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Create(ctx, options)
	if err != nil {
		return backportOutput{}, fmt.Errorf("pushed branch %s, but could not open its pull request into %s: %w; to retry, switch to it and run gt pr backport --continue", result.Branch, result.Target, err)
	}
	if err := git.RecordBackportPull(ctx, a.deps.Git, a.options.cwd, result.Branch, item.Number); err != nil {
		a.warn(cmd, "Could not record pull request #%d in branch %s: %v", item.Number, result.Branch, err)
	}
	a.done(cmd, "Backported #%d to %s in %s", pull.Number, result.Target, refLabel(t.Repo, item.Number, item.Title))
	return backportOutput{BackportResult: result, Pull: item}, nil
}

// reportBackports writes the opened pull requests: their URLs, or with --json the backports.
func (a *application) reportBackports(cmd *cobra.Command, outputs []backportOutput) error {
	if a.options.json != "" {
		if outputs == nil {
			outputs = []backportOutput{}
		}
		return a.writeJSON(cmd, outputs)
	}
	for _, output := range outputs {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), terminal.SafeLine(output.Pull.URL)); err != nil {
			return err
		}
	}
	return nil
}
