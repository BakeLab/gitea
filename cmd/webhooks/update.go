// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhooks

import (
	stdctx "context"
	"fmt"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWebhooksUpdate represents a sub command of webhooks to update webhook
var CmdWebhooksUpdate = cli.Command{
	Name:        "update",
	Aliases:     []string{"edit", "u"},
	Usage:       "Update a webhook",
	Description: "Update webhook configuration in repository, organization, or globally",
	ArgsUsage:   "<webhook-id>",
	Action:      runWebhooksUpdate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "url",
			Usage: "webhook URL",
		},
		&cli.StringFlag{
			Name:  "secret",
			Usage: "webhook secret",
		},
		&cli.StringFlag{
			Name:  "events",
			Usage: "comma separated list of events",
		},
		&cli.BoolFlag{
			Name:  "active",
			Usage: "webhook is active",
		},
		&cli.BoolFlag{
			Name:  "inactive",
			Usage: "webhook is inactive",
		},
		&cli.StringFlag{
			Name:  "branch-filter",
			Usage: "branch filter for push events",
		},
		&cli.StringFlag{
			Name:  "authorization-header",
			Usage: "authorization header",
		},
	}, flags.AllDefaultFlags...),
}

func runWebhooksUpdate(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("webhook ID is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	webhookID, err := utils.ArgToIndex(cmd.Args().First())
	if err != nil {
		return err
	}

	// Get current webhook to preserve existing settings
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

	// Update configuration
	config := hook.Config
	if config == nil {
		config = make(map[string]string)
	}

	if cmd.IsSet("url") {
		config["url"] = cmd.String("url")
	}
	if cmd.IsSet("secret") {
		config["secret"] = cmd.String("secret")
	}
	if cmd.IsSet("branch-filter") {
		config["branch_filter"] = cmd.String("branch-filter")
	}
	if cmd.IsSet("authorization-header") {
		config["authorization_header"] = cmd.String("authorization-header")
	}

	// Update events if specified
	events := hook.Events
	if cmd.IsSet("events") {
		eventsList := strings.Split(cmd.String("events"), ",")
		events = make([]string, len(eventsList))
		for i, event := range eventsList {
			events[i] = strings.TrimSpace(event)
		}
	}

	// Update active status
	active := hook.Active
	if cmd.IsSet("active") {
		active = cmd.Bool("active")
	} else if cmd.IsSet("inactive") {
		active = !cmd.Bool("inactive")
	}

	if c.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(c.Org) > 0 {
		_, err = client.EditOrgHook(c.Org, int64(webhookID), gitea.EditHookOption{
			Config: config,
			Events: events,
			Active: &active,
		})
	} else {
		_, err = client.EditRepoHook(c.Owner, c.Repo, int64(webhookID), gitea.EditHookOption{
			Config: config,
			Events: events,
			Active: &active,
		})
	}
	if err != nil {
		return err
	}

	fmt.Printf("Webhook %d updated successfully\n", webhookID)
	return nil
}
