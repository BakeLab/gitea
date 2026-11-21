// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"

	"gitea.dev/cli/internal/services"

	"github.com/spf13/cobra"
)

// pullActionCommand builds a command that does one thing to a pull request, such as lock it, and
// reports it like gt pr close does.
func (a *application) pullActionCommand(verb, short, long, example, past string, complete cobra.CompletionFunc, already func(services.Pull) string, act func(ctx context.Context, service services.Issues, number int64) error) *cobra.Command {
	return &cobra.Command{
		Use: verb + " [<number> | <url> | <branch>]", Short: short, Long: long, Example: example,
		Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), ValidArgsFunction: complete,
		RunE: func(cmd *cobra.Command, args []string) error {
			t, n, err := a.pullSelectorAny(cmd, args)
			if err != nil {
				return err
			}
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
			if err != nil {
				return err
			}
			if already != nil && already(item) != "" {
				a.warn(cmd, "Pull request %s is already %s", refLabel(t.Repo, n, item.Title), already(item))
				return a.pullResult(cmd, t, item)
			}
			if err := act(cmd.Context(), services.Issues{Client: t.client, Repository: t.Repo, Pulls: true}, n); err != nil {
				return err
			}
			a.done(cmd, "%s pull request %s", past, refLabel(t.Repo, n, item.Title))
			return a.pullResult(cmd, t, item)
		},
	}
}

func (a *application) pullLockCommand() *cobra.Command {
	var reason string
	cmd := a.pullActionCommand("lock", "Lock the conversation of a pull request",
		`Lock the conversation of a pull request, so that only people with write access can comment on
it. One that is locked already is reported and keeps its reason.

Gitea does not check the reason: it offers Too heated, Off-topic, Resolved and Spam by default,
and administrators can change that.`,
		`$ gt pr lock 13
$ gt pr lock 13 --reason "Too heated"`, "Locked", firstArg(a.completePulls("open")),
		func(item services.Pull) string {
			if item.Locked {
				return "locked"
			}
			return ""
		},
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetLocked(ctx, n, true, reason)
		})
	cmd.Flags().StringVarP(&reason, "reason", "r", "", "Reason for locking, shown on the pull request, such as \"Resolved\"")
	return cmd
}

func (a *application) pullUnlockCommand() *cobra.Command {
	return a.pullActionCommand("unlock", "Unlock the conversation of a pull request",
		"Unlock the conversation of a pull request, so that everyone who can see it can comment again.",
		"$ gt pr unlock 13", "Unlocked", firstArg(a.completePulls("all")), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetLocked(ctx, n, false, "")
		})
}

func (a *application) pullPinCommand() *cobra.Command {
	return a.pullActionCommand("pin", "Pin a pull request to the top of the list",
		"Pin a pull request to the top of the repository's pull request list. Gitea allows only a few pinned items at a time.",
		"$ gt pr pin 13", "Pinned", firstArg(a.completePulls("open")), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetPinned(ctx, n, true)
		})
}

func (a *application) pullUnpinCommand() *cobra.Command {
	return a.pullActionCommand("unpin", "Unpin a pull request",
		"Unpin a pull request from the top of the repository's pull request list.",
		"$ gt pr unpin 13", "Unpinned", firstArg(a.completePulls("all")), nil,
		func(ctx context.Context, service services.Issues, n int64) error {
			return service.SetPinned(ctx, n, false)
		})
}

func (a *application) pullDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use: "delete [<number> | <url> | <branch>]", Aliases: []string{"rm"}, Short: "Delete a pull request",
		Long: `Delete a pull request for good, with its comments, reviews and attachments. This cannot be
undone and needs the permission to delete issues, which Gitea gives repository owners and
administrators. The head and base branches stay. To only stop work on it, use gt pr close.
A branch names only an open pull request: give the number of a closed or merged one.
Issues are not deleted by this command: see gt issue delete.`,
		Example: `$ gt pr delete 13
$ gt pr delete 13 --yes`,
		Annotations: pullArgumentsAnnotation(map[string]string{"gt.text": "pull request deletion does not return a resource", "gt.confirm": "true"}),
		Args:        usageArgs(cobra.MaximumNArgs(1)), ValidArgsFunction: firstArg(a.completePulls("all")),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, n, err := a.pullSelector(cmd, args) // a branch never names a closed one, which may be long gone
			if err != nil {
				return err
			}
			item, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
			if err != nil {
				return err
			}
			service := services.Issues{Client: t.client, Repository: t.Repo, Pulls: true}
			return a.deleteConfirmed(cmd, "pull request "+refLabel(t.Repo, n, item.Title), func() error { return service.Delete(cmd.Context(), n) })
		},
	}
}
