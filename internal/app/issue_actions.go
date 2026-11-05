// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// issueActionCommand builds a command that does one thing to each issue named, such as lock it,
// and reports it like gt issue close does. Gitea applies these to pull requests too. already, if
// given, tells when an issue needs nothing done, and how to describe it.
func (a *application) issueActionCommand(verb, short, long, example, past string, complete cobra.CompletionFunc, already func(services.Issue) string, act func(ctx context.Context, service services.Issues, number int64) error) *cobra.Command {
	return &cobra.Command{
		Use: verb + " {<number> | <url>}...", Short: short, Long: long, Example: example,
		Annotations: map[string]string{"gt.arguments": issueArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, numbers, err := a.resolveRefs(cmd, args, issueRef)
			if err != nil {
				return err
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			results, failures := applyBatch(cmd, numbers, issueLabel, func(n int64) (services.Issue, error) {
				item, err := service.View(cmd.Context(), n)
				if err != nil {
					return item, itemError(err, target.Repo)
				}
				if already != nil && already(item) != "" {
					a.warn(cmd, "%s %s is already %s", titleCase(discussionNoun(item.Kind)), refLabel(target.Repo, n, item.Title), already(item))
					return item, nil
				}
				if err := act(cmd.Context(), service, n); err != nil {
					return item, itemError(err, target.Repo)
				}
				a.done(cmd, "%s %s %s", past, discussionNoun(item.Kind), refLabel(target.Repo, n, item.Title))
				return item, nil
			}, nil)
			return errors.Join(append(failures, writeBatchJSON(a, cmd, len(numbers), results))...)
		},
	}
}

func (a *application) issueLockCommand() *cobra.Command {
	var reason string
	cmd := a.issueActionCommand("lock", "Lock the conversation of issues",
		`Lock the conversation of issues or pull requests, so that only people with write access can
comment on them. One that is locked already is reported and keeps its reason.

Gitea does not check the reason: it offers Too heated, Off-topic, Resolved and Spam by default,
and administrators can change that.`,
		`$ gt issue lock 12
$ gt issue lock 12 13 --reason "Too heated"`, "Locked", a.completeIssues("open"),
		func(item services.Issue) string {
			if item.Locked {
				return "locked"
			}
			return ""
		},
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetLocked(ctx, n, true, reason)
		})
	cmd.Flags().StringVarP(&reason, "reason", "r", "", "Reason for locking, shown on the issue, such as \"Resolved\"")
	return cmd
}

func (a *application) issueUnlockCommand() *cobra.Command {
	return a.issueActionCommand("unlock", "Unlock the conversation of issues",
		"Unlock the conversation of issues or pull requests, so that everyone who can see them can comment again.",
		"$ gt issue unlock 12", "Unlocked", a.completeIssues("all"), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetLocked(ctx, n, false, "")
		})
}

func (a *application) issuePinCommand() *cobra.Command {
	return a.issueActionCommand("pin", "Pin issues to the top of the list",
		"Pin issues or pull requests to the top of the repository's lists. Gitea allows only a few pinned items at a time.",
		"$ gt issue pin 12", "Pinned", a.completeIssues("open"), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetPinned(ctx, n, true)
		})
}

func (a *application) issueUnpinCommand() *cobra.Command {
	return a.issueActionCommand("unpin", "Unpin issues",
		"Unpin issues or pull requests from the top of the repository's lists.",
		"$ gt issue unpin 12", "Unpinned", a.completeIssues("all"), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetPinned(ctx, n, false)
		})
}

func (a *application) issueDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete {<number> | <url>}...", Aliases: []string{"rm"}, Short: "Delete issues",
		Long: `Delete issues for good, with their comments, attachments and time tracking. This cannot be
undone and needs the permission to delete issues, which Gitea gives repository owners and
administrators. To only stop work on one, use gt issue close. Pull requests are not deleted.`,
		Example: `$ gt issue delete 12
$ gt issue delete 12 13 --yes`,
		Annotations: map[string]string{"gt.arguments": issueArguments, "gt.text": "issue deletion does not return a resource", "gt.confirm": "true"},
		Args:        usageArgs(cobra.MinimumNArgs(1)), ValidArgsFunction: a.completeIssues("all"),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, numbers, err := a.resolveRefs(cmd, args, issueRef)
			if err != nil {
				return err
			}
			service := services.Issues{Client: target.client, Repository: target.Repo}
			// Look at every issue first, so that one that cannot be deleted stops the batch before any is.
			items := make([]services.Issue, len(numbers))
			for i, n := range numbers {
				if items[i], err = service.View(cmd.Context(), n); err != nil {
					return fmt.Errorf("%s: %w", issueLabel(n), itemError(err, target.Repo))
				}
				if items[i].Kind == services.KindPull {
					return fmt.Errorf("%s is a pull request; gt does not delete pull requests, use gt pr close", refLabel(target.Repo, n, items[i].Title))
				}
			}
			prompt := "Delete issue " + refLabel(target.Repo, numbers[0], items[0].Title) + "?"
			if len(numbers) > 1 {
				prompt = fmt.Sprintf("Delete %d issues of %s (%s)?", len(numbers), target.Repo, strings.Join(shortRefs(numbers), ", "))
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: prompt}); err != nil {
				return err
			}
			deleted, failures := applyBatch(cmd, items, func(item services.Issue) string { return issueLabel(item.Number) }, func(item services.Issue) (services.Issue, error) {
				if err := service.Delete(cmd.Context(), item.Number); err != nil {
					return item, itemError(err, target.Repo)
				}
				return item, nil
			}, func(item services.Issue) {
				a.done(cmd, "Deleted issue %s", refLabel(target.Repo, item.Number, item.Title))
			})
			if len(failures) > 0 && !a.deps.Streams.OutTTY { // done was silent, and scripts must learn what is gone
				for _, item := range deleted {
					a.warn(cmd, "Deleted issue %s", refLabel(target.Repo, item.Number, item.Title))
				}
			}
			return errors.Join(failures...)
		},
	}
}

// shortRefs writes issue numbers as #12.
func shortRefs(numbers []int64) []string {
	refs := make([]string, len(numbers))
	for i, n := range numbers {
		refs[i] = fmt.Sprintf("#%d", n)
	}
	return refs
}
