// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"

	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/print"
	"code.gitea.io/tea/modules/utils"

	"code.gitea.io/sdk/gitea"
	"github.com/urfave/cli/v3"
)

// editPullState abstracts the arg parsing to edit the given pull request
func editPullState(_ stdctx.Context, cmd *cli.Command, opts gitea.EditPullRequestOption) error {
	ctx := context.InitCommand(cmd)
	ctx.Ensure(context.CtxRequirement{RemoteRepo: true})
	if ctx.Args().Len() == 0 {
		return fmt.Errorf("Please provide a Pull Request index")
	}

	indices, err := utils.ArgsToIndices(ctx.Args().Slice())
	if err != nil {
		return err
	}

	client := ctx.Login.Client()
	for _, index := range indices {
		pr, _, err := client.EditPullRequest(ctx.Owner, ctx.Repo, index, opts)
		if err != nil {
			return err
		}

		if len(indices) > 1 {
			fmt.Println(pr.HTMLURL)
		} else {
			print.PullDetails(pr, nil, nil)
		}
	}
	return nil
}
