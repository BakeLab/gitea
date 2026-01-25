// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package labels

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v3"
)

// CmdLabelDelete represents a sub command of labels to delete label.
var CmdLabelDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete a label",
	Description: `Delete a label`,
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runLabelDelete,
	Flags: append([]cli.Flag{
		&cli.Int64Flag{
			Name:     "id",
			Usage:    "label id",
			Required: true,
		},
	}, flags.AllDefaultFlags...),
}

func runLabelDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	labelID := ctx.Int64("id")
	client := ctx.Login.Client()

	// Verify the label exists first
	label, _, err := client.GetRepoLabel(ctx.Owner, ctx.Repo, labelID)
	if err != nil {
		return fmt.Errorf("failed to get label %d: %w", labelID, err)
	}

	_, err = client.DeleteLabel(ctx.Owner, ctx.Repo, labelID)
	if err != nil {
		return fmt.Errorf("failed to delete label '%s' (id: %d): %w", label.Name, labelID, err)
	}

	fmt.Printf("Label '%s' (id: %d) deleted successfully\n", label.Name, labelID)
	return nil
}
