// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	stdctx "context"
	"fmt"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/debug"
	"gitea.dev/tea/modules/git"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"

	"github.com/urfave/cli/v3"
)

// CmdRepoClone represents a sub command of repos to create a local copy
var CmdRepoClone = cli.Command{
	Name:    "clone",
	Aliases: []string{"C"},
	Usage:   "Clone a repository locally",
	Description: `Clone a repository locally, without a local git installation required.
The repo slug can be specified in different formats:
	gitea/tea
	tea
	gitea.com/gitea/tea
	git@gitea.com:gitea/tea
	https://gitea.com/gitea/tea
	ssh://gitea.com:22/gitea/tea
When a host is specified in the repo-slug, it will override the login specified with --login.
	`,
	Category:  catHelpers,
	Action:    runRepoClone,
	ArgsUsage: "<repo-slug> [target dir]",
	Flags: []cli.Flag{
		&cli.IntFlag{
			Name:    "depth",
			Aliases: []string{"d"},
			Usage:   "num commits to fetch, defaults to all",
		},
		&flags.LoginFlag,
	},
}

func runRepoClone(ctx stdctx.Context, cmd *cli.Command) error {
	teaCmd, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}

	args := teaCmd.Args()
	if args.Len() < 1 {
		return cli.ShowCommandHelp(ctx, cmd, "clone")
	}
	dir := args.Get(1)

	var (
		login *config.Login = teaCmd.Login
		owner string
		repo  string
	)

	// parse first arg as repo specifier
	repoSlug := args.Get(0)
	url, err := git.ParseURL(repoSlug)
	if err != nil {
		return err
	}

	debug.Printf("Cloning repository %s into %s", url.String(), dir)

	owner, repo = utils.GetOwnerAndRepo(url.Path, login.User)
	if url.Host != "" {
		var lookupErr error
		login, lookupErr = config.GetLoginByHost(url.Host)
		if lookupErr != nil {
			return lookupErr
		}
		if login == nil {
			return fmt.Errorf("no login configured matching host '%s', run 'tea login add' first", url.Host)
		}
		debug.Printf("Matched login '%s' for host '%s'", login.Name, url.Host)
	}

	_, err = task.RepoClone(
		ctx,
		dir,
		login,
		owner,
		repo,
		interact.PromptPassword,
		teaCmd.Int("depth"),
	)

	return err
}
