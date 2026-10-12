// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

func (a *application) milestoneStateCommand(verb string, service func(*cobra.Command) (services.Milestones, error)) *cobra.Command {
	var byName bool
	cmd := &cobra.Command{Use: verb + " <id-or-name>...", Short: titleCase(verb) + " one or more milestones", Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		ids := make(map[string]int64, len(args))
		seenIDs := map[int64]bool{}
		selectID := func(argument string, id int64) error {
			if seenIDs[id] {
				return clierr.Wrap(clierr.Usage, errors.New("selectors resolve to the same milestone"))
			}
			seenIDs[id], ids[argument] = true, id
			return nil
		}
		for _, argument := range args {
			if _, seen := ids[argument]; seen {
				return clierr.Wrap(clierr.Usage, errors.New("milestone selectors must be unique"))
			}
			id, err := milestoneSelector(argument, byName)
			if err != nil {
				return err
			}
			ids[argument] = 0
			if !byName {
				if err := selectID(argument, id); err != nil {
					return err
				}
			}
		}
		milestones, err := service(cmd)
		if err != nil {
			return err
		}
		if byName {
			for _, argument := range args {
				id, err := milestones.ResolveName(cmd.Context(), argument)
				if err != nil {
					return err
				}
				if err := selectID(argument, id); err != nil {
					return err
				}
			}
		}
		state := "closed"
		if verb == "reopen" {
			state = "open"
		}
		return batch(a, cmd, args, func(argument string) string { return "milestone " + argument }, func(argument string) (services.Milestone, error) {
			return milestones.Edit(cmd.Context(), ids[argument], services.MilestoneOptions{State: &state})
		}, milestoneRow)
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Select by exact milestone titles instead of IDs")
	return cmd
}
