// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/AlecAivazis/survey/v2"
	"github.com/urfave/cli/v3"
)

// CmdRepoRm represents a sub command of repos to delete an existing repo
var CmdRepoRm = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete an existing repository",
	Description: "Removes a repository from Create a repository from an existing repo",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      runRepoDelete,
	Flags: append([]cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Aliases:  []string{""},
			Required: true,
			Usage:    "name of the repo",
		},
		&cli.StringFlag{
			Name:     "owner",
			Aliases:  []string{"O"},
			Required: false,
			Usage:    "owner of the repo",
		},
		&cli.BoolFlag{
			Name:     "force",
			Aliases:  []string{"f"},
			Required: false,
			Value:    false,
			Usage:    "Force the deletion and don't ask for confirmation",
		},
	}, flags.LoginOutputFlags...),
}

func runRepoDelete(_ stdctx.Context, cmd *cli.Command) error {
	ctx := context.InitCommand(cmd)

	client := ctx.Login.Client()

	var owner string
	if ctx.IsSet("owner") {
		owner = ctx.String("owner")

	} else {
		owner = ctx.Login.User
	}

	repoName := ctx.String("name")

	repoSlug := fmt.Sprintf("%s/%s", owner, repoName)

	if !ctx.Bool("force") {
		var enteredRepoSlug string
		promptRepoName := &survey.Input{
			Message: fmt.Sprintf("Confirm the deletion of the repository '%s' by typing its name: ", repoSlug),
		}
		if err := survey.AskOne(promptRepoName, &enteredRepoSlug, survey.WithValidator(survey.Required)); err != nil {
			return err
		}

		if enteredRepoSlug != repoSlug {
			return fmt.Errorf("Entered wrong repository name '%s', expected '%s'", enteredRepoSlug, repoSlug)
		}
	}

	_, err := client.DeleteRepo(owner, repoName)
	if err != nil {
		return err
	}

	fmt.Printf("Successfully deleted %s/%s\n", owner, repoName)
	return nil
}
