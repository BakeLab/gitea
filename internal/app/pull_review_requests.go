// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) pullReviewRequestCommand(add bool) *cobra.Command {
	use, short, verb := "cancel-request [<number> | <url> | <branch>]", "Withdraw review requests", "Withdrew review request from"
	if add {
		use, short, verb = "request [<number> | <url> | <branch>]", "Request reviews from users or teams", "Requested review from"
	}
	var input services.ReviewRequestInput
	cmd := &cobra.Command{Use: use, Short: short, Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		trimNames(input.Users, input.Teams)
		changes := services.ReviewRequestChanges{}
		if add {
			changes.Add = input
		} else {
			changes.Remove = input
		}
		if err := changes.Validate(); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		t.expandMeAll(input.Users)
		item, err := (services.Reviews{Client: t.client, Repository: t.Repo}).ChangeRequests(cmd.Context(), n, changes)
		if err != nil {
			return err
		}
		names := append([]string{}, input.Users...)
		for _, team := range input.Teams {
			names = append(names, "team "+team)
		}
		a.done(cmd, "%s %s on %s", verb, strings.Join(names, ", "), refLabel(t.Repo, item.Number, item.Title))
		if a.options.json != "" {
			return a.writeJSON(cmd, item)
		}
		return nil
	}}
	cmd.Flags().StringSliceVarP(&input.Users, "reviewer", "r", nil, "Reviewer usernames or email addresses; @me for yourself")
	cmd.Flags().StringSliceVar(&input.Teams, "team", nil, "Teams of the repository's organization")
	_ = cmd.RegisterFlagCompletionFunc("reviewer", a.completeUsers())
	cmd.ValidArgsFunction = a.completePulls("open")
	if add {
		cmd.Example = `$ gt pr review request 13 --reviewer carol,dave
$ gt pr review request --team developers`
	} else {
		cmd.Example = "$ gt pr review cancel-request 13 --reviewer carol"
	}
	return cmd
}
