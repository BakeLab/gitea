// Copyright 2020 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package releases

import (
	"fmt"

	"code.gitea.io/tea/cmd/flags"
	"code.gitea.io/tea/modules/context"

	"github.com/urfave/cli/v2"
)

// CmdReleaseDelete represents a sub command of Release to delete a release
var CmdReleaseDelete = cli.Command{
	Name:        "delete",
	Aliases:     []string{"rm"},
	Usage:       "Delete one or more releases",
	Description: `Delete one or more releases`,
	ArgsUsage:   "<release tag> [<release tag>...]",
	Action:      runReleaseDelete,
	Flags: append([]cli.Flag{
		&cli.BoolFlag{
			Name:    "confirm",
			Aliases: []string{"y"},
			Usage:   "Confirm deletion (required)",
		},
		&cli.BoolFlag{
			Name:  "delete-tag",
			Usage: "Also delete the git tag for this release",
		},
	}, flags.AllDefaultFlags...),
}

func runReleaseDelete(cmd *cli.Context) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	client := ctx.Login.Client()

	if !ctx.Args().Present() {
		fmt.Println("Release tag needed to edit")
		return nil
	}

	if !ctx.Bool("confirm") {
		fmt.Println("Are you sure? Please confirm with -y or --confirm.")
		return nil
	}

	for _, tag := range ctx.Args().Slice() {
		release, err := getReleaseByTag(ctx.Owner, ctx.Repo, tag, client)
		if err != nil {
			return err
		}
		_, err = client.DeleteRelease(ctx.Owner, ctx.Repo, release.ID)
		if err != nil {
			return err
		}

		if ctx.Bool("delete-tag") {
			_, err = client.DeleteTag(ctx.Owner, ctx.Repo, tag)
			return err
		}
	}

	return nil
}
