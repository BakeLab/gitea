// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package releases

import (
	stdctx "context"

	"gitea.dev/sdk"

	"gitea.dev/tea/cmd/flags"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/print"
	"github.com/urfave/cli/v3"
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
func RunReleasesList(requestCtx stdctx.Context, cmd *cli.Command) error {
	ctx, err := context.InitCommand(cmd)
	if err != nil {
		return err
	}
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	releases, _, err := ctx.Login.Client().Releases.ListReleases(requestCtx, ctx.Owner, ctx.Repo, gitea.ListReleasesOptions{
		ListOptions: flags.GetListOptions(cmd),
	})
	if err != nil {
		return err
	}

	return print.ReleasesList(releases, ctx.Output)
}
