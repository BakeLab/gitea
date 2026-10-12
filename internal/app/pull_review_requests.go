// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) pullReviewRequestCommand(add bool) *cobra.Command {
	verb := "cancel-request"
	if add {
		verb = "request"
	}
	var input services.ReviewRequestInput
	cmd := &cobra.Command{Use: verb + " <number>", Short: titleCase(verb) + " reviews from users or organization teams", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		n, err := number(args[0])
		if err != nil {
			return err
		}
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
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		item, err := (services.Reviews{Client: target.client, Repository: target.Repo}).ChangeRequests(cmd.Context(), n, changes)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, pullRecord)
	}}
	cmd.Flags().StringSliceVar(&input.Users, "reviewer", nil, "Reviewer usernames or email addresses")
	cmd.Flags().StringSliceVar(&input.Teams, "team", nil, "Team names within the repository's organization")
	return cmd
}
