// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	stdctx "context"
	"fmt"

	"gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"github.com/urfave/cli/v3"
)

// CmdRepoFork represents a sub command of repos to fork an existing repo
var CmdRepoFork = cli.Command{
	Name:        "fork",
	Aliases:     []string{"f"},
	Usage:       "Fork an existing repository",
	Description: "Create a repository from an existing repo",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runRepoFork,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:    "owner",
			Aliases: []string{"O"},
			Usage:   "name of fork's owner, defaults to current user",
		},
	}, flags.LoginRepoFlags...),
}

func runRepoFork(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}
	client := ctx.Login.Client()

	opts := gitea.CreateForkOption{}
	if ctx.IsSet("owner") {
		owner := ctx.String("owner")
		opts.Organization = &owner
	}

	repo, _, err := client.Repositories.CreateFork(requestCtx, ctx.Owner, ctx.Repo, opts)
	if err != nil {
		return err
	}

	topics, _, err := client.Repositories.ListRepoTopics(requestCtx, repo.Owner.UserName, repo.Name, gitea.ListRepoTopicsOptions{})
	if err != nil {
		return err
	}
	print.RepoDetails(repo, topics)

	fmt.Printf("%s\n", repo.HTMLURL)
	return nil
}
