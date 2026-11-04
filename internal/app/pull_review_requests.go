// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"slices"
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
		if err := changes.Validate(); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		service := services.Reviews{Client: t.client, Repository: t.Repo}
		reviews, err := service.All(cmd.Context(), n)
		if err != nil {
			return err
		}
		item, err := service.ChangeRequests(cmd.Context(), n, changes)
		if err != nil {
			return err
		}
		ref := refLabel(t.Repo, item.Number, item.Title)
		users, teams := services.RequestedReviewers(reviews)
		var changed []string
		for _, group := range []struct {
			names, held []string
			prefix      string
		}{{input.Users, users, ""}, {input.Teams, teams, "team "}} {
			for _, name := range group.names {
				email := strings.Contains(name, "@") // the reviews name users by login only
				held := !email && slices.ContainsFunc(group.held, func(h string) bool { return strings.EqualFold(h, name) })
				switch {
				case add && held:
					a.warn(cmd, "%s%s already has a review request on %s", group.prefix, name, ref)
				case !add && !held && !email:
					a.warn(cmd, "%s%s has no review request on %s", group.prefix, name, ref)
				default:
					changed = append(changed, group.prefix+name)
				}
			}
		}
		if len(changed) > 0 {
			a.done(cmd, "%s %s on %s", verb, strings.Join(changed, ", "), ref)
		}
		return a.pullResult(cmd, t, item)
	}}
	cmd.Flags().StringSliceVarP(&input.Users, "reviewer", "r", nil, "Reviewer usernames or email addresses; @me for yourself")
	cmd.Flags().StringSliceVar(&input.Teams, "team", nil, "Teams of the repository's organization")
	_ = cmd.RegisterFlagCompletionFunc("reviewer", a.completeUsers())
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	if add {
		cmd.Example = `$ gt pr review request 13 --reviewer carol,dave
$ gt pr review request --team developers`
	} else {
		cmd.Example = "$ gt pr review cancel-request 13 --reviewer carol"
	}
	return jsonOutput[services.Pull](cmd)
}
