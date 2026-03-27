// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	stdctx "context"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdRepoEdit represents a sub command of repos to edit one
var CmdRepoEdit = cli.Command{
	Name:        "edit",
	Aliases:     []string{"e"},
	Usage:       "Edit repository properties",
	Description: "Edit repository properties",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runRepoEdit,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:  "name",
			Usage: "New name of the repository",
		},
		&cli.StringFlag{
			Name:    "description",
			Aliases: []string{"desc"},
			Usage:   "New description of the repository",
		},
		&cli.StringFlag{
			Name:  "website",
			Usage: "New website URL of the repository",
		},
		&cli.StringFlag{
			Name:        "private",
			Usage:       "Set private [true/false]",
			DefaultText: "true",
		},
		&cli.StringFlag{
			Name:        "template",
			Usage:       "Set template [true/false]",
			DefaultText: "true",
		},
		&cli.StringFlag{
			Name:        "archived",
			Usage:       "Set archived [true/false]",
			DefaultText: "true",
		},
		&cli.StringFlag{
			Name:  "default-branch",
			Usage: "Set default branch",
		},
	}, flags.AllDefaultFlags...),
}

func runRepoEdit(_ stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	opts := gitea.EditRepoOption{}

	if ctx.IsSet("name") {
		val := ctx.String("name")
		opts.Name = &val
	}
	if ctx.IsSet("description") {
		val := ctx.String("description")
		opts.Description = &val
	}
	if ctx.IsSet("website") {
		val := ctx.String("website")
		opts.Website = &val
	}
	if ctx.IsSet("default-branch") {
		val := ctx.String("default-branch")
		opts.DefaultBranch = &val
	}
	if ctx.IsSet("private") {
		opts.Private = gitea.OptionalBool(strings.ToLower(ctx.String("private"))[:1] == "t")
	}
	if ctx.IsSet("template") {
		opts.Template = gitea.OptionalBool(strings.ToLower(ctx.String("template"))[:1] == "t")
	}
	if ctx.IsSet("archived") {
		opts.Archived = gitea.OptionalBool(strings.ToLower(ctx.String("archived"))[:1] == "t")
	}

	repo, _, err := client.EditRepo(ctx.Owner, ctx.Repo, opts)
	if err != nil {
		return err
	}

	topics, _, err := client.ListRepoTopics(repo.Owner.UserName, repo.Name, gitea.ListRepoTopicsOptions{})
	if err != nil {
		return err
	}
	print.RepoDetails(repo, topics)
	return nil
}
