// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	"fmt"
	"strings"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/context"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"
)

// runPullReview handles the common logic for approving/rejecting pull requests
func runPullReview(ctx *context.TeaContext, state gitea.ReviewStateType, requireComment bool) error {
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	minArgs := 1
	if requireComment {
		minArgs = 2
	}

	if ctx.Args().Len() < minArgs {
		if requireComment {
			return fmt.Errorf("pull request index and comment are required")
		}
		return fmt.Errorf("pull request index is required")
	}

	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	comment := strings.Join(ctx.Args().Tail(), " ")

	return task.CreatePullReview(ctx, idx, state, comment, nil)
}
