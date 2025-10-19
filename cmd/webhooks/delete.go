// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhooks

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWebhooksDelete represents a sub command of webhooks to delete webhook
var CmdWebhooksDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete a webhook",
	Description: "Delete a webhook by ID from repository, organization, or globally",
	ArgsUsage:   "<webhook-id>",
	Action:      runWebhooksDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "confirm deletion without prompting",
		},
	}, flags.AllDefaultFlags...),
}

func runWebhooksDelete(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("webhook ID is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	webhookID, err := utils.ArgToIndex(cmd.Args().First())
	if err != nil {
		return err
	}

	// Get webhook details first to show what we're deleting
	var hook *gitea.Hook
	if c.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(c.Org) > 0 {
		hook, _, err = client.GetOrgHook(c.Org, int64(webhookID))
	} else {
		hook, _, err = client.GetRepoHook(c.Owner, c.Repo, int64(webhookID))
	}
	if err != nil {
		return err
	}

	if !cmd.Bool("confirm") {
		fmt.Printf("Are you sure you want to delete webhook %d (%s)? [y/N] ", hook.ID, hook.Config["url"])
		var response string
		fmt.Scanln(&response)
		if response != "y" && response != "Y" && response != "yes" {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	if c.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(c.Org) > 0 {
		_, err = client.DeleteOrgHook(c.Org, int64(webhookID))
	} else {
		_, err = client.DeleteRepoHook(c.Owner, c.Repo, int64(webhookID))
	}
	if err != nil {
		return err
	}

	fmt.Printf("Webhook %d deleted successfully\n", webhookID)
	return nil
}
