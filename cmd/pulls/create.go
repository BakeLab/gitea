// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"

	gitea "gitea.dev/sdk"
	"github.com/urfave/cli/v3"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"
)

// CmdPullsCreate creates a pull request
var CmdPullsCreate = cli.Command{
	Name:        "create",
	Aliases:     []string{"c"},
	Usage:       "Create a pull-request",
	Description: "Create a pull-request in the current repo",
	Action:      runPullsCreate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "head",
			Usage: "Branch name of the PR source (default is current one). To specify a different head repo, use <user>:<branch>",
		},
		&cli.StringFlag{
			Name:    "base",
			Aliases: []string{"b"},
			Usage:   "Branch name of the PR target (default is repos default branch)",
		},
		&cli.BoolFlag{
			Name:    "allow-maintainer-edits",
			Aliases: []string{"edits"},
			Usage:   "Enable maintainers to push to the base branch of created pull",
			Value:   true,
		},
		&cli.BoolFlag{
			Name:  "agit",
			Usage: "Create an agit flow pull request",
		},
		&cli.StringFlag{
			Name:  "topic",
			Usage: "Topic name for agit flow pull request",
		},
		&cli.BoolFlag{
			Name:  "draft",
			Usage: "Create as a draft (prepends \"WIP: \" to the title; Gitea treats WIP-prefixed PRs as drafts)",
		},
	}, flags.IssuePRCreateFlags...),
}

func runPullsCreate(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	// Interactive mode and head-branch defaulting both need a local repo.
	// When --head is given explicitly the user can target a cross-fork PR
	// from outside a working tree (e.g. with --repo <owner>/<repo>);
	// task.CreatePull only consults ctx.LocalRepo when head is empty.
	needsLocalRepo := ctx.IsInteractiveMode() || len(ctx.String("head")) == 0
	requirement := context.CtxRequirement{RemoteRepo: true}
	if needsLocalRepo {
		requirement.LocalRepo = true
	}
	if err := ctx.Ensure(requirement); err != nil {
		return err
	}

	// no args -> interactive mode
	if ctx.IsInteractiveMode() {
		if err := interact.CreatePull(requestCtx, ctx); err != nil && !interact.IsQuitting(err) {
			return err
		}
		return nil
	}

	// else use args to create PR
	opts, err := flags.GetIssuePRCreateFlags(requestCtx, ctx)
	if err != nil {
		return err
	}

	if ctx.Bool("draft") {
		opts.Title = utils.AddDraftPrefix(opts.Title)
	}

	if ctx.Bool("agit") {
		return task.CreateAgitFlowPull(
			requestCtx,
			ctx,
			ctx.String("remote"),
			ctx.String("head"),
			ctx.String("base"),
			ctx.String("topic"),
			opts,
			interact.PromptPassword,
		)
	}

	var allowMaintainerEdits *bool
	if ctx.IsSet("allow-maintainer-edits") {
		allowMaintainerEdits = gitea.OptionalBool(ctx.Bool("allow-maintainer-edits"))
	}

	return task.CreatePull(
		requestCtx,
		ctx,
		ctx.String("base"),
		ctx.String("head"),
		allowMaintainerEdits,
		opts,
	)
}
