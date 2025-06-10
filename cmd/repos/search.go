// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repos

import (
	stdctx "context"
	"fmt"
	"strings"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// CmdReposSearch represents a sub command of repos to find them
var CmdReposSearch = cli.Command{
	Name:        "search",
	Aliases:     []string{"s"},
	Usage:       "Find any repo on an Gitea instance",
	Description: "Find any repo on an Gitea instance",
	ArgsUsage:   "[<search term>]",
	Action:      runReposSearch,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			// TODO: it might be nice to search for topics as an ADDITIONAL filter.
			// for that, we'd probably need to make multiple queries and UNION the results.
			Name:     "topic",
			Aliases:  []string{"t"},
			Required: false,
			Usage:    "Search for term in repo topics instead of name",
		},
		&typeFilterFlag,
		&cli.StringFlag{
			Name:     "owner",
			Aliases:  []string{"O"},
			Required: false,
			Usage:    "Filter by owner",
		},
		&cli.StringFlag{
			Name:     "private",
			Required: false,
			Usage:    "Filter private repos (true|false)",
		},
		&cli.StringFlag{
			Name:     "archived",
			Required: false,
			Usage:    "Filter archived repos (true|false)",
		},
		repoFieldsFlag,
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.LoginOutputFlags...),
}

func runReposSearch(_ stdctx.Context, cmd *cli.Command) error {
	teaCmd := context.InitCommand(cmd)
	client := teaCmd.Login.Client()

	var ownerID int64
	if teaCmd.IsSet("owner") {
		// test if owner is a organisation
		org, _, err := client.GetOrg(teaCmd.String("owner"))
		if err != nil {
			// HACK: the client does not return a response on 404, so we can't check res.StatusCode
			if err.Error() != "404 Not Found" {
				return fmt.Errorf("Could not find owner: %s", err)
			}

			// if owner is no org, its a user
			user, _, err := client.GetUserInfo(teaCmd.String("owner"))
			if err != nil {
				return err
			}
			ownerID = user.ID
		} else {
			ownerID = org.ID
		}
	}

	var isArchived *bool
	if teaCmd.IsSet("archived") {
		archived := strings.ToLower(teaCmd.String("archived"))[:1] == "t"
		isArchived = &archived
	}

	var isPrivate *bool
	if teaCmd.IsSet("private") {
		private := strings.ToLower(teaCmd.String("private"))[:1] == "t"
		isPrivate = &private
	}

	mode, err := getTypeFilter(cmd)
	if err != nil {
		return err
	}

	var keyword string
	if teaCmd.Args().Present() {
		keyword = strings.Join(teaCmd.Args().Slice(), " ")
	}

	user, _, err := client.GetMyUserInfo()
	if err != nil {
		return err
	}

	rps, _, err := client.SearchRepos(gitea.SearchRepoOptions{
		ListOptions:          teaCmd.GetListOptions(),
		OwnerID:              ownerID,
		IsPrivate:            isPrivate,
		IsArchived:           isArchived,
		Type:                 mode,
		Keyword:              keyword,
		KeywordInDescription: true,
		KeywordIsTopic:       teaCmd.Bool("topic"),
		PrioritizedByOwnerID: user.ID,
	})
	if err != nil {
		return err
	}

	fields, err := repoFieldsFlag.GetValues(cmd)
	if err != nil {
		return err
	}
	print.ReposList(rps, teaCmd.Output, fields)
	return nil
}
