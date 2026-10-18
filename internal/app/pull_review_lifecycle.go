// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullReviewSubmitCommand() *cobra.Command {
	var kind reviewType
	var body, file string
	var useEditor bool
	cmd := &cobra.Command{Use: "submit [<number> | <url> | <branch>] [<review-id>]", Short: "Submit your pending review", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		state, err := kind.state()
		if err != nil {
			return err
		}
		if state == "" {
			return errReviewType(cmd)
		}
		missingBody := clierr.Wrap(clierr.Usage, errors.New("a change request needs a body; pass --body, --body-file or --editor"))
		var supplied *string
		if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
			if body, err = a.readBody(cmd, body, file); err != nil {
				return err
			}
			if state == "REQUEST_CHANGES" && strings.TrimSpace(body) == "" {
				return missingBody
			}
			supplied = &body
		}
		var id int64
		if len(args) == 2 {
			if id, err = number(args[1]); err != nil {
				return err
			}
		}
		t, n, err := a.pullSelector(cmd, args[:min(len(args), 1)])
		if err != nil {
			return err
		}
		pull, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
		if err != nil {
			return err
		}
		if err := ownReview(pull, t, state); err != nil {
			return err
		}
		service := services.Reviews{Client: t.client, Repository: t.Repo}
		if id == 0 {
			if id, err = a.pendingReview(cmd, service, t, n, state); err != nil {
				return err
			}
		}
		if useEditor || (supplied == nil && state == "REQUEST_CHANGES") {
			current, err := service.View(cmd.Context(), n, id)
			if err != nil {
				return err
			}
			supplied = &current.Body
			if useEditor {
				if draft, err = a.editDraft(cmd, current.Body); err != nil {
					return err
				}
				supplied = &draft.Body
			}
		}
		if state == "REQUEST_CHANGES" && strings.TrimSpace(*supplied) == "" {
			return missingBody
		}
		item, err := service.Submit(cmd.Context(), n, id, services.ReviewSubmitOptions{Event: state, Body: supplied})
		if err != nil {
			return err
		}
		return a.reportReview(cmd, t, pull, item)
	}}
	kind.register(cmd, false)
	cmd.Flags().StringVarP(&body, "body", "b", "", "Review body (default: the body of the pending review)")
	cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Edit the body of the pending review in your editor")
	cmd.Example = `# Start a pending review, then submit it as an approval
$ gt pr review 13 --pending -b "First pass" --comments-file inline.json
$ gt pr review submit 13 --approve

# Submit a pending review by its ID
$ gt pr review submit 13 21 --request-changes -b "See the inline comments"`
	cmd.ValidArgsFunction = a.completeReviewArgs(false)
	return cmd
}

// pendingReview finds the pending review that the account started on pull request n.
func (a *application) pendingReview(cmd *cobra.Command, service services.Reviews, t target, n int64, state string) (int64, error) {
	pending, err := service.Pending(cmd.Context(), n, t.login())
	if err != nil {
		return 0, err
	}
	switch len(pending) {
	case 0:
		flag := map[string]string{"APPROVED": "--approve", "REQUEST_CHANGES": "--request-changes", "COMMENT": "--comment"}[state]
		return 0, fmt.Errorf("no pending review by %s on %s#%d; to post a new review run: gt pr review %d %s", t.login(), t.Repo, n, n, flag)
	case 1:
		return pending[0].ID, nil
	}
	ids := make([]string, len(pending))
	for i, review := range pending {
		ids[i] = strconv.FormatInt(review.ID, 10)
	}
	return 0, clierr.Wrap(clierr.Usage, fmt.Errorf("%s has several pending reviews on %s#%d (%s); pass a review ID", t.login(), t.Repo, n, strings.Join(ids, ", ")))
}

func (a *application) pullReviewDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "delete <number> <review-id>", Short: "Delete a review", Annotations: pullArgumentRequiredAnnotation(textOutput("review deletion does not return a resource")), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, id, err := a.pullAndID(cmd, args)
		if err != nil {
			return err
		}
		service := services.Reviews{Client: t.client, Repository: t.Repo}
		item, err := service.View(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("review %d by %s on %s#%d", id, item.Reviewer(), t.Repo, n)
		return a.deleteConfirmed(cmd, subject, func() error { return service.Delete(cmd.Context(), n, id) })
	}}
	cmd.Annotations["gt.confirm"] = "true"
	cmd.Example = "$ gt pr review delete 13 21"
	cmd.ValidArgsFunction = a.completeReviewArgs(false)
	return cmd
}

func (a *application) pullReviewDismissCommand(dismiss bool) *cobra.Command {
	var body, file string
	var useEditor bool
	use, short := "undismiss <number> <review-id>", "Restore a dismissed review"
	if dismiss {
		use, short = "dismiss <number> <review-id>", "Dismiss an approval or change request"
	}
	cmd := &cobra.Command{Use: use, Short: short, Annotations: pullArgumentRequiredAnnotation(nil), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		var err error
		// The reason is collected before any request when it comes from flags or cannot be
		// prompted for; an editor or prompt waits until the review is known to be dismissable.
		collect := func() error {
			switch {
			case useEditor:
				draft, err = a.editDraft(cmd, "")
				body = draft.Body
			case cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file"):
				body, err = a.readBody(cmd, body, file)
			default:
				_, err = a.requireInput(cmd, &body, "body", "Reason for dismissing the review", nil)
			}
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("a dismissal needs a reason; pass --body, --body-file or --editor"))
			}
			return nil
		}
		collected := false
		if dismiss {
			if err := validateBodySources(cmd); err != nil {
				return err
			}
			snapshot, err := a.settings()
			if err != nil {
				return err
			}
			flagged := cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
			if !useEditor && (flagged || !a.policy(snapshot).CanPrompt(a.deps.Streams)) {
				if err := collect(); err != nil {
					return err
				}
				collected = true
			}
		}
		t, n, id, err := a.pullAndID(cmd, args)
		if err != nil {
			return err
		}
		service := services.Reviews{Client: t.client, Repository: t.Repo}
		current, err := service.View(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		subject := fmt.Sprintf("%s on %s#%d", reviewPossessive(current), t.Repo, n)
		switch {
		case dismiss && current.State != "APPROVED" && current.State != "REQUEST_CHANGES":
			// Gitea refuses other review types with a misleading "permission denied".
			return clierr.Wrap(clierr.Usage, fmt.Errorf("only approvals and change requests can be dismissed; %s is not one", subject))
		case !dismiss && !current.Dismissed:
			a.warn(cmd, "%s is not dismissed", subject)
			return a.jsonResult(cmd, current)
		}
		if dismiss && !collected {
			if err := collect(); err != nil {
				return err
			}
		}
		if dismiss {
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Dismiss " + subject + "?"}); err != nil {
				return err
			}
		}
		item, err := service.Dismiss(cmd.Context(), n, id, dismiss, body)
		if err != nil {
			return err
		}
		if dismiss {
			a.done(cmd, "Dismissed %s", subject)
		} else {
			a.done(cmd, "Restored %s", subject)
		}
		return a.jsonResult(cmd, item)
	}}
	if dismiss {
		cmd.Annotations["gt.confirm"] = "true"
		cmd.Flags().StringVarP(&body, "body", "b", "", "Reason for dismissing the review (required)")
		cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the reason from `file`, or - for stdin")
		cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Write the reason in your editor")
		cmd.Example = `$ gt pr review dismiss 13 6 -b "Approved an outdated revision"`
	} else {
		cmd.Example = "$ gt pr review undismiss 13 6"
	}
	cmd.ValidArgsFunction = a.completeReviewArgs(false)
	return cmd
}
