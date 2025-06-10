// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package milestones

import (
	"context"

	"code.gitea.io/tea/cmd/flags"
	"github.com/urfave/cli/v3"
)

// CmdMilestonesClose represents a sub command of milestones to close an milestone
var CmdMilestonesClose = cli.Command{
	Name:        "close",
	Usage:       "Change state of one or more milestones to 'closed'",
	Description: `Change state of one or more milestones to 'closed'`,
	ArgsUsage:   "<milestone name> [<milestone name>...]",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		if cmd.Bool("force") {
			return deleteMilestone(ctx, cmd)
		}
		return editMilestoneStatus(ctx, cmd, true)
	},
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "force",
			Aliases: []string{"f"},
			Usage:   "delete milestone",
		},
	}, flags.AllDefaultFlags...),
}
