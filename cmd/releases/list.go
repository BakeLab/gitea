// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package releases

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v2"
)

// CmdReleaseList represents a sub command of Release to list releases
var CmdReleaseList = cli.Command{
	Name:        "list",
	Aliases:     []string{"ls"},
	Usage:       "List Releases",
	Description: "List Releases",
	ArgsUsage:   " ", // command does not accept arguments
	Action:      RunReleasesList,
	Flags: append([]cli.Flag{
		&flags.PaginationPageFlag,
		&flags.PaginationLimitFlag,
	}, flags.AllDefaultFlags...),
}

// RunReleasesList list releases
func RunReleasesList(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})

	releases, _, err := ctx.Login.Client().ListReleases(ctx.Owner, ctx.Repo, gitea.ListReleasesOptions{
		ListOptions: ctx.GetListOptions(),
	})
	if err != nil {
		return err
	}

	print.ReleasesList(releases, ctx.Output)
	return nil
}

func getReleaseByTag(owner, repo, tag string, client *gitea.Client) (*gitea.Release, error) {
	rl, _, err := client.ListReleases(owner, repo, gitea.ListReleasesOptions{
		ListOptions: gitea.ListOptions{Page: -1},
	})
	if err != nil {
		return nil, err
	}
	if len(rl) == 0 {
		return nil, fmt.Errorf("Repo does not have any release")
	}
	for _, r := range rl {
		if r.TagName == tag {
			return r, nil
		}
	}
	return nil, fmt.Errorf("Release tag does not exist")
}
