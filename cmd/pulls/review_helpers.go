// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pulls

import (
	stdctx "context"
	"errors"
	"fmt"
	"io"
	"strings"

	gitea "gitea.dev/sdk"

	"gitea.dev/tea/modules/config"
	"gitea.dev/tea/modules/context"
	"gitea.dev/tea/modules/interact"
	"gitea.dev/tea/modules/task"
	"gitea.dev/tea/modules/theme"
	"gitea.dev/tea/modules/utils"

	"charm.land/huh/v2"
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

// runPullReviewReply handles replying to a specific review comment on a pull request.
func runPullReviewReply(requestCtx stdctx.Context, ctx *context.TeaContext) error {
	if err := ctx.Ensure(context.CtxRequirement{RemoteRepo: true}); err != nil {
		return err
	}

	if ctx.Args().Len() < 2 {
		return fmt.Errorf("pull request index and comment ID are required")
	}

	idx, err := utils.ArgToIndex(ctx.Args().First())
	if err != nil {
		return err
	}

	commentID, err := utils.ArgToIndex(ctx.Args().Get(1))
	if err != nil {
		return err
	}

	body, err := getCommentBody(ctx, ctx.Args().Slice()[2:], "Reply(markdown):", "reply")
	if err != nil {
		return err
	}

	return task.ReplyToPullReviewComment(requestCtx, ctx, idx, commentID, body)
}

func getCommentBody(ctx *context.TeaContext, extraArgs []string, promptTitle, noun string) (string, error) {
	body := strings.Join(extraArgs, " ")
	if interact.IsStdinPiped() {
		bodyStdin, err := io.ReadAll(ctx.Reader)
		if err != nil {
			return "", err
		}
		if len(bodyStdin) != 0 {
			body = strings.Join([]string{body, string(bodyStdin)}, "\n\n")
		}
	} else if len(body) == 0 {
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title(promptTitle).
					ExternalEditor(config.GetPreferences().Editor).
					EditorExtension("md").
					Value(&body),
			),
		).WithTheme(theme.GetTheme()).Run(); err != nil {
			return "", err
		}
	}

	if len(strings.TrimSpace(body)) == 0 {
		return "", errors.New("no " + noun + " content provided")
	}

	return body, nil
}
