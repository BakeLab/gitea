// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// ensureHeadPublished prompts to push the head branch when the selected remote tip is missing or differs.
// Cross-repo heads (owner:branch), non-work-trees, and repositories without a matching remote are skipped.
func (a *application) ensureHeadPublished(cmd *cobra.Command, t target, snapshot settings.Snapshot, head string) error {
	if head == "" || strings.Contains(head, ":") {
		return nil
	}
	ctx := cmd.Context()
	probe := a.gitProbe()
	inside, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--is-inside-work-tree")
	if err != nil || inside != "true" {
		return nil
	}
	local, err := probe.Output(ctx, a.options.cwd, false, "rev-parse", "--verify", "refs/heads/"+head+"^{commit}")
	if err != nil {
		return nil
	}
	remote, err := a.checkoutRemote(ctx, t, snapshot)
	if err != nil {
		if a.options.remote == "" && snapshot.Preferences.Remote == "" && errors.Is(err, errNoMatchingRemote) {
			return nil
		}
		return clierr.Wrap(clierr.Usage, err)
	}
	remoteTip, tipErr := probe.Output(ctx, a.options.cwd, false, "for-each-ref", "--format=%(objectname)", "refs/remotes/"+remote+"/"+head)
	if tipErr == nil && remoteTip == local {
		return nil
	}
	if err := a.confirm(cmd, fmt.Sprintf("Push branch %q to remote %q before creating the pull request?", head, remote)); err != nil {
		return err
	}
	credentials, err := a.remoteCredentials(ctx, t, remote)
	if err != nil {
		return err
	}
	args := append(append([]string{}, credentials...), "push", "--", remote, "refs/heads/"+head)
	_, err = a.git(ctx, args...)
	return err
}

func pullRecord(item services.Pull) terminal.Record {
	return numberedRecord(item.Number, item.Title, item.URL)
}

func (a *application) addPullMutations(group *cobra.Command) {
	group.AddCommand(a.pullCreateCommand())
	for _, verb := range []string{"close", "reopen"} {
		group.AddCommand(&cobra.Command{Use: verb + " <number>", Short: titleCase(verb) + " a pull request", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			t, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			state := "closed"
			if verb == "reopen" {
				state = "open"
			}
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Edit(cmd.Context(), n, services.PullEdit{State: &state})
			if err != nil {
				return err
			}
			return writeRecord(a, cmd, item, pullRecord)
		}})
	}
	group.AddCommand(&cobra.Command{Use: "diff <number>", Short: "Print a pull request diff", Annotations: textOutput("diff returns patch text"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		body, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Diff(cmd.Context(), n)
		if err != nil {
			return err
		}
		defer body.Close()
		_, err = io.Copy(terminal.SafeWriter(cmd.OutOrStdout(), a.deps.Streams.OutTTY, false), body)
		return err
	}})
	group.AddCommand(a.pullReviewCommand())
	var method, mergeTitle, mergeMessage, mergeMessageFile string
	var deleteBranch, mergeEditor bool
	merge := &cobra.Command{Use: "merge [number]", Short: "Merge a pull request", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var editorDraft terminal.Draft
		defer finishDraft(&editorDraft, &runErr)
		if method != "merge" && method != "rebase" && method != "rebase-merge" && method != "squash" && method != "fast-forward-only" {
			return clierr.Wrap(clierr.Usage, errors.New("unsupported merge method"))
		}
		if err := validateTextSources(cmd, "message", "message-file"); err != nil {
			return err
		}
		if !mergeEditor {
			var err error
			mergeMessage, err = a.readTextSource(cmd, mergeMessage, mergeMessageFile, "message", "message-file")
			if err != nil {
				return err
			}
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		policy := a.policy(snapshot)
		if len(args) == 0 && (!policy.CanPrompt(a.deps.Streams) || a.deps.MergeForm == nil) {
			return clierr.Wrap(clierr.Usage, errors.New("supply a pull request number, or run interactively to choose one"))
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		var n int64
		if len(args) == 1 {
			n, err = number(args[0])
			if err != nil {
				return err
			}
		} else {
			n, err = a.selectPullToMerge(cmd, t)
			if err != nil {
				return err
			}
		}
		if mergeEditor {
			editorDraft, err = a.editDraft(cmd, "")
			if err != nil {
				return err
			}
			mergeMessage = editorDraft.Body
		}
		if err := a.confirm(cmd, fmt.Sprintf("Merge pull request %s#%d", t.Repo, n)); err != nil {
			return err
		}
		if !cmd.Flags().Changed("delete-branch") && !a.options.yes && policy.CanPrompt(a.deps.Streams) {
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
			if err != nil {
				return err
			}
			if !item.HeadRemoteDeleted {
				if deleteBranch, err = a.deps.Prompter.Confirm(cmd.Context(), fmt.Sprintf("Delete branch %s after merging?", item.Head)); err != nil {
					return err
				}
			}
		}
		return (services.Pulls{Client: t.client, Repository: t.Repo}).Merge(cmd.Context(), n, services.MergeOptions{
			Method: method, Title: mergeTitle, Message: mergeMessage, DeleteBranch: deleteBranch,
		})
	}}
	merge.Flags().StringVarP(&method, "method", "s", "merge", "Merge strategy")
	merge.Flags().StringVar(&method, "style", "merge", "Alias of --method")
	_ = merge.Flags().MarkHidden("style")
	merge.Flags().StringVar(&mergeTitle, "title", "", "Merge commit title")
	merge.Flags().StringVar(&mergeMessage, "message", "", "Merge commit message")
	merge.Flags().StringVar(&mergeMessageFile, "message-file", "", "Read merge commit message from file, or - for stdin")
	merge.Flags().BoolVar(&mergeEditor, "editor", false, "Compose the merge commit message in the configured editor")
	merge.Flags().BoolVar(&deleteBranch, "delete-branch", false, "Delete head branch after merging")
	group.AddCommand(merge)
}

func (a *application) selectPullToMerge(cmd *cobra.Command, t target) (int64, error) {
	branch, err := a.currentBranch(cmd.Context())
	if err != nil {
		return 0, clierr.Wrap(clierr.Usage, errors.New("supply a pull number outside a branch checkout"))
	}
	items, err := (services.Pulls{Client: t.client, Repository: t.Repo}).ListOpen(cmd.Context())
	if err != nil {
		return 0, err
	}
	var headMatches, baseMatches []terminal.MergePullChoice
	for _, item := range items {
		label := item.Head
		if owner, _, ok := strings.Cut(item.HeadRepo, "/"); ok && !strings.EqualFold(item.HeadRepo, t.Repo) {
			label = owner + ":" + item.Head // fork heads can share a branch name with the local one
		}
		choice := terminal.MergePullChoice{Number: item.Number, Title: item.Title, Head: label}
		switch branch {
		case item.Head:
			headMatches = append(headMatches, choice)
		case item.Base:
			baseMatches = append(baseMatches, choice)
		}
	}
	return a.deps.MergeForm.SelectPull(cmd.Context(), append(headMatches, baseMatches...))
}
