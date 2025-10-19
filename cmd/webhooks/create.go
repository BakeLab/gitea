// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhooks

import (
	stdctx "context"
	"fmt"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWebhooksCreate represents a sub command of webhooks to create webhook
var CmdWebhooksCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create a webhook",
	Description: "Create a webhook in repository, organization, or globally",
	ArgsUsage:   "<webhook-url>",
	Action:      runWebhooksCreate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "type",
			Usage: "webhook type (gitea, gogs, slack, discord, dingtalk, telegram, msteams, feishu, wechatwork, packagist)",
			Value: "gitea",
		},
		&cli.StringFlag{
			Name:  "secret",
			Usage: "webhook secret",
		},
		&cli.StringFlag{
			Name:  "events",
			Usage: "comma separated list of events",
			Value: "push",
		},
		&cli.BoolFlag{
			Name:  "active",
			Usage: "webhook is active",
			Value: true,
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

func runWebhooksCreate(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		return fmt.Errorf("webhook URL is required")
	}

	c := context.InitCommand(cmd)
	client := c.Login.Client()

	webhookType := gitea.HookType(cmd.String("type"))
	url := cmd.Args().First()
	secret := cmd.String("secret")
	active := cmd.Bool("active")
	branchFilter := cmd.String("branch-filter")
	authHeader := cmd.String("authorization-header")

	// Parse events
	eventsList := strings.Split(cmd.String("events"), ",")
	events := make([]string, len(eventsList))
	for i, event := range eventsList {
		events[i] = strings.TrimSpace(event)
	}

	config := map[string]string{
		"url":          url,
		"http_method":  "post",
		"content_type": "json",
	}

	if secret != "" {
		config["secret"] = secret
	}

	if branchFilter != "" {
		config["branch_filter"] = branchFilter
	}

	if authHeader != "" {
		config["authorization_header"] = authHeader
	}

	var hook *gitea.Hook
	var err error
	if c.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(c.Org) > 0 {
		hook, _, err = client.CreateOrgHook(c.Org, gitea.CreateHookOption{
			Type:   webhookType,
			Config: config,
			Events: events,
			Active: active,
		})
	} else {
		hook, _, err = client.CreateRepoHook(c.Owner, c.Repo, gitea.CreateHookOption{
			Type:   webhookType,
			Config: config,
			Events: events,
			Active: active,
		})
	}
	if err != nil {
		return err
	}

	fmt.Printf("Webhook created successfully (ID: %d)\n", hook.ID)
	return nil
}
