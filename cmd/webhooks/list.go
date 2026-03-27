// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhooks

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdWebhooksList represents a sub command of webhooks to list webhooks
var CmdWebhooksList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List webhooks",
	Description: "List webhooks in repository, organization, or globally",
	Action:      RunWebhooksList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunWebhooksList list webhooks
func RunWebhooksList(ctx stdctx.Context, cmd *cli.Command) error {
	c, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	client := c.Login.Client()

	var hooks []*gitea.Hook
	if c.IsGlobal {
		return fmt.Errorf("global webhooks not yet supported in this version")
	} else if len(c.Org) > 0 {
		hooks, _, err = client.ListOrgHooks(c.Org, gitea.ListHooksOptions{
			ListOptions: flags.GetListOptions(cmd),
		})
	} else {
		hooks, _, err = client.ListRepoHooks(c.Owner, c.Repo, gitea.ListHooksOptions{
			ListOptions: flags.GetListOptions(cmd),
		})
	}
	if err != nil {
		return err
	}

	return print.WebhooksList(hooks, c.Output)
}
