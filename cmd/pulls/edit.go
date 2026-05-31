// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"
	"github.com/urfave/cli/v3"
)

// applyDraftFlag mutates opts.Title according to --draft / --ready.
// If a flag is set but --title isn't, it fetches the current title from the server.
// Returns an error if both --draft and --ready are set, or on a server error.
func applyDraftFlag(requestCtx stdctx.Context, ctx *context.TeaContext, client *gitea.Client, idx int64, opts *task.EditIssueOption) error {
	draft := ctx.Bool("draft")
	ready := ctx.Bool("ready")
	if !draft && !ready {
		return nil
	}
	if draft && ready {
		return fmt.Errorf("--draft and --ready are mutually exclusive")
	}

	var current string
	if opts.Title != nil {
		current = *opts.Title
	} else {
		pr, _, err := client.PullRequests.GetPullRequest(requestCtx, ctx.Owner, ctx.Repo, idx)
		if err != nil {
			return fmt.Errorf("could not fetch pull request #%d: %s", idx, err)
		}
		current = pr.Title
	}

	var next string
	if draft {
		next = utils.AddDraftPrefix(current)
	} else {
		next = utils.StripDraftPrefix(current)
	}
	if next != current || opts.Title != nil {
		opts.Title = &next
	}
	return nil
}

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
		&cli.BoolFlag{
			Name:  "draft",
			Usage: "Mark as draft by prepending \"WIP: \" to the title (idempotent)",
		},
		&cli.BoolFlag{
			Name:  "ready",
			Usage: "Mark as ready for review by stripping any leading \"WIP: \" or \"[WIP]\" prefix",
		},
	),
}

func runPullsEdit(requestCtx stdctx.Context, cmd *cli.Command) error {
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
		if err := applyDraftFlag(requestCtx, ctx, client, opts.Index, opts); err != nil {
			return err
		}
		pr, err := task.EditPull(requestCtx, ctx, client, *opts)
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
func editPullState(requestCtx stdctx.Context, cmd *cli.Command, opts gitea.EditPullRequestOption) error {
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
		pr, _, err := client.PullRequests.EditPullRequest(requestCtx, ctx.Owner, ctx.Repo, index, opts)
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
