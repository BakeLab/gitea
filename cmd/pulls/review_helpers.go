// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"fmt"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/utils"
)

// runPullReview handles the common logic for approving/rejecting pull requests
func runPullReview(requestCtx stdctx.Context, ctx *context.TeaContext, state gitea.ReviewStateType, requireComment bool) error {
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

	return task.CreatePullReview(requestCtx, ctx, idx, state, comment, nil)
}

// runResolveComment handles the common logic for resolving/unresolving review comments
func runResolveComment(requestCtx stdctx.Context, ctx *context.TeaContext, action func(stdctx.Context, *context.TeaContext, int64) error) error {
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() < 1 {
		return fmt.Errorf("comment ID is required")
	}

	commentID, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	return action(requestCtx, ctx, commentID)
}
