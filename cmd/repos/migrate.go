// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdRepoMigrate represents a sub command of repos to migrate one
var CmdRepoMigrate = cli.Command{
	Name:        "migrate",
	Aliases:     []string{"m"},
	Usage:       "Migrate a repository",
	Description: "Migrate a repository and or mirror it.",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runRepoMigrate,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Usage:    "Name of the repository",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "owner",
			Usage:    "Owner of the repository",
			Required: true,
		},
		&cli.StringFlag{
			Name:     "clone-url",
			Usage:    "Clone URL of the repository",
			Required: true,
		},
		&cli.StringFlag{
			Name: "service",
			Usage: string("Service to migrate from. Supported services are: " + gitea.GitServicePlain +
				", " + gitea.GitServiceGitea + ", " + gitea.GitServiceGitlab + ", " + gitea.GitServiceGogs),
			Required: true,
		},
		&cli.BoolFlag{
			Name:  "mirror",
			Usage: "Mirror the repository",
		},
		&cli.BoolFlag{
			Name:  "private",
			Usage: "Make the repository private",
		},
		&cli.BoolFlag{
			Name:  "template",
			Usage: "Make the repository a template",
		},
		&cli.BoolFlag{
			Name:  "wiki",
			Usage: "Copy the wiki",
		},
		&cli.BoolFlag{
			Name:  "issues",
			Usage: "Copy the issues",
		},
		&cli.BoolFlag{
			Name:  "pull-requests",
			Usage: "Copy the pull requests",
		},
		&cli.BoolFlag{
			Name:  "releases",
			Usage: "Copy the releases",
		},
		&cli.BoolFlag{
			Name:  "milestones",
			Usage: "Copy the milestones",
		},
		&cli.StringFlag{
			Name:  "mirror-interval",
			Usage: "Interval to mirror the repository.",
		},
		&cli.BoolFlag{
			Name:  "lfs",
			Usage: "Copy the LFS objects",
		},
		&cli.StringFlag{
			Name:  "lfs-endpoint",
			Usage: "LFS endpoint to use",
		},
		&cli.StringFlag{
			Name:  "auth-user",
			Usage: "Username to use for authentication.",
		},
		&cli.StringFlag{
			Name:  "auth-password",
			Usage: "Password to use for authentication.",
		},
		&cli.StringFlag{
			Name:  "auth-token",
			Usage: "Token to use for authentication.",
		},
	}, flags.LoginOutputFlags...),
}

func runRepoMigrate(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)
	client := ctx.Login.Client()
	var (
		repo    *gitea.Repository
		err     error
		service gitea.GitServiceType
	)

	if ctx.IsSet("service") {
		switch ctx.String("service") {
		case "git":
			service = gitea.GitServicePlain
		case "gitea":
			service = gitea.GitServiceGitea
		case "gitlab":
			service = gitea.GitServiceGitlab
		case "gogs":
			service = gitea.GitServiceGogs
		case "github":
			service = gitea.GitServiceGithub
		default:
			return fmt.Errorf("unknown git service type '%s'", ctx.String("service"))
		}
	}

	opts := gitea.MigrateRepoOption{
		RepoName:       ctx.String("name"),
		RepoOwner:      ctx.String("owner"),
		CloneAddr:      ctx.String("clone-url"),
		Service:        service,
		AuthUsername:   ctx.String("auth-user"),
		AuthPassword:   ctx.String("auth-password"),
		AuthToken:      ctx.String("auth-token"),
		Mirror:         ctx.Bool("mirror"),
		Private:        ctx.Bool("private"),
		Description:    ctx.String("description"),
		Wiki:           ctx.Bool("wiki"),
		Milestones:     ctx.Bool("milestones"),
		Labels:         ctx.Bool("labels"),
		Issues:         ctx.Bool("issues"),
		PullRequests:   ctx.Bool("pull-requests"),
		Releases:       ctx.Bool("releases"),
		MirrorInterval: ctx.String("mirror-interval"),
		LFS:            ctx.Bool("lfs"),
		LFSEndpoint:    ctx.String("lfs-endpoint"),
	}

	repo, _, err = client.MigrateRepo(opts)

	if err != nil {
		return err
	}

	topics, _, err := client.ListRepoTopics(repo.Owner.UserName, repo.Name, gitea.ListRepoTopicsOptions{})
	if err != nil {
		return err
	}
	print.RepoDetails(repo, topics)

	fmt.Printf("%s\n", repo.HTMLURL)
	return nil
}
