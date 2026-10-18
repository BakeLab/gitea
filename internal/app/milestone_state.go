// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

// milestoneStateCommand closes or reopens milestones, reporting those already in that state instead
// of changing them.
func (a *application) milestoneStateCommand(verb string, service milestoneService) *cobra.Command {
	var byName bool
	state, past, short := "closed", "Closed", "Close milestones"
	if verb == "reopen" {
		state, past, short = "open", "Reopened", "Reopen milestones"
	}
	cmd := &cobra.Command{Use: verb + " <milestone>...", Short: short, Example: fmt.Sprintf(`$ gt milestone %[1]s v1.0
$ gt milestone %[1]s v1.0 v1.1`, verb), Annotations: map[string]string{"gt.arguments": milestoneArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if slices.ContainsFunc(args, func(arg string) bool { return strings.TrimSpace(arg) == "" }) {
			return clierr.Wrap(clierr.Usage, errors.New("milestone title or ID cannot be empty"))
		}
		milestones, t, err := service(cmd)
		if err != nil {
			return err
		}
		selected, err := milestones.Find(cmd.Context(), args, byName)
		if err != nil {
			return err
		}
		for i, milestone := range selected {
			if slices.ContainsFunc(selected[:i], func(other services.Milestone) bool { return other.ID == milestone.ID }) {
				return clierr.Wrap(clierr.Usage, errors.New("milestone "+args[i]+" is selected more than once"))
			}
		}
		results, failures := applyBatch(cmd, selected, func(milestone services.Milestone) string { return "milestone " + milestone.Title }, func(milestone services.Milestone) (services.Milestone, error) {
			if milestone.State == state {
				a.warn(cmd, "%s is already %s", titleCase(milestoneName(t, milestone)), state)
				return milestone, nil
			}
			item, err := milestones.Edit(cmd.Context(), milestone.ID, services.MilestoneOptions{State: &state})
			if err == nil {
				a.done(cmd, "%s %s", past, milestoneName(t, item))
			}
			return item, err
		}, nil)
		return errors.Join(append(failures, writeBatchJSON(a, cmd, len(selected), results))...)
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match numeric arguments as milestone titles rather than IDs")
	// Only milestones that are not in the target state are worth completing.
	cmd.ValidArgsFunction = a.completeMilestonesIn(map[string]string{"open": "closed", "closed": "open"}[state])
	return cmd
}
