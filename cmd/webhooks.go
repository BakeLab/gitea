// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/webhooks"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWebhooks represents the webhooks command
var CmdWebhooks = cli.Command{
	Name:        "webhooks",
	Aliases:     []string{"webhook", "hooks", "hook"},
	Category:    catEntities,
	Usage:       "Manage webhooks",
	Description: "List, create, update, and delete repository, organization, or global webhooks",
	ArgsUsage:   "[webhook-id]",
	Action:      runWebhooksDefault,
	Commands: []*cli.Command{
		&webhooks.CmdWebhooksList,
		&webhooks.CmdWebhooksCreate,
		&webhooks.CmdWebhooksDelete,
		&webhooks.CmdWebhooksUpdate,
	},
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "repo",
			Usage: "repository to operate on",
		},
		&cli.StringFlag{
			Name:  "org",
			Usage: "organization to operate on",
		},
		&cli.BoolFlag{
			Name:  "global",
			Usage: "operate on global webhooks",
		},
		&cli.StringFlag{
			Name:  "login",
			Usage: "gitea login instance to use",
		},
		&cli.StringFlag{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "output format [table, csv, simple, tsv, yaml, json]",
		},
	}, webhooks.CmdWebhooksList.Flags...),
}

func runWebhooksDefault(ctx stdctx.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 1 {
		return runWebhookDetail(ctx, cmd)
	}
	return webhooks.RunWebhooksList(ctx, cmd)
}

func runWebhookDetail(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)
	client := ctx.Login.Client()

	webhookID, err := utils.ArgToIndex(cmd.Args().First())
	if err != nil {
		return err
	}

	var hook *gitea.Hook
	if ctx.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(ctx.Org) > 0 {
		hook, _, err = client.GetOrgHook(ctx.Org, int64(webhookID))
	} else {
		hook, _, err = client.GetRepoHook(ctx.Owner, ctx.Repo, int64(webhookID))
	}
	if err != nil {
		return err
	}

	print.WebhookDetails(hook)
	return nil
}
