// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdPullsEdit is the subcommand of pulls to edit pull requests
var CmdPullsEdit = cli.Command{
	Name:    "edit",
	Aliases: []string{"e"},
	Usage:   "Edit one or more pull requests",
	Description: `Edit one or more pull requests. To unset a property again,
use an empty string (eg. --milestone "").`,
	ArgsUsage: "<idx> [<idx>...]",
	Action:    runPullsEdit,
	Flags: append(flags.IssuePREditFlags,
		&cli.StringFlag{
			Name:    "add-reviewers",
			Aliases: []string{"r"},
			Usage:   "Comma-separated list of usernames to request review from",
		},
		&cli.StringFlag{
			Name:  "remove-reviewers",
			Usage: "Comma-separated list of usernames to remove from reviewers",
		},
	),
}

func runPullsEdit(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if !cmd.Args().Present() {
		return fmt.Errorf("must specify at least one pull request index")
	}

	opts, err := flags.GetIssuePREditFlags(ctx)
	if err != nil {
		return err
	}

	if cmd.IsSet("add-reviewers") {
		opts.AddReviewers = strings.Split(cmd.String("add-reviewers"), ",")
	}
	if cmd.IsSet("remove-reviewers") {
		opts.RemoveReviewers = strings.Split(cmd.String("remove-reviewers"), ",")
	}

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, opts.Index = range indices {
		pr, err := task.EditPull(ctx, client, *opts)
		if err != nil {
			return err
		}
		if ctx.Args().Len() > 1 {
			fmt.Println(pr.HTMLURL)
		} else {
			print.PullDetails(pr, nil, nil)
		}
	}

	return nil
}

// editPullState abstracts the arg parsing to edit the given pull request
func editPullState(_ stdctx.Context, cmd *cli.Command, opts gitea.EditPullRequestOption) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	if ctx.Args().Len() == 0 {
		return fmt.Errorf("pull request index is required")
	}

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, index := range indices {
		pr, _, err := client.EditPullRequest(ctx.Owner, ctx.Repo, index, opts)
		if err != nil {
			return err
		}

		if len(indices) > 1 {
			fmt.Println(pr.HTMLURL)
		} else {
			print.PullDetails(pr, nil, nil)
		}
	}
	return nil
}
