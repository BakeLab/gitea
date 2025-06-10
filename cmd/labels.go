// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"

	"code.gitea.io/tea/cmd/labels"
	"github.com/urfave/cli/v3"
)

// CmdLabels represents to operate repositories' labels.
var CmdLabels = cli.Command{
	Name:        "labels",
	Aliases:     []string{"label"},
	Category:    catEntities,
	Usage:       "Manage issue labels",
	Description: `Manage issue labels`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runLabels,
	Commands: []*cli.Command{
		&labels.CmdLabelsList,
		&labels.CmdLabelCreate,
		&labels.CmdLabelUpdate,
		&labels.CmdLabelDelete,
	},
	Flags: labels.CmdLabelsList.Flags,
}

func runLabels(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runLabelsDetails(cmd)
	}
	return labels.RunLabelsList(ctx, cmd)
}

func runLabelsDetails(cmd *cli.Command) error {
	return fmt.Errorf("Not yet implemented")
}
