// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullReviewLifecycleCommand(verb string) *cobra.Command {
	var body, file, event string
	var useEditor bool
	cmd := &cobra.Command{Use: verb + " <number> <review-id>", Short: titleCase(verb) + " a pull request review", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		n, err := number(args[0])
		if err != nil {
			return err
		}
		id, err := number(args[1])
		if err != nil {
			return err
		}
		if verb == "submit" {
			if err := oneOf("event", event, "APPROVED", "REQUEST_CHANGES", "COMMENT"); err != nil {
				return err
			}
		}
		var suppliedBody *string
		if verb == "submit" || verb == "dismiss" {
			body, err = a.readBody(cmd, body, file)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
				suppliedBody = &body
			}
			if verb == "dismiss" {
				if suppliedBody == nil && !useEditor {
					if _, err := a.require(cmd, &body, "body"); err != nil {
						return err
					}
				}
				if strings.TrimSpace(body) == "" && !useEditor {
					return clierr.Wrap(clierr.Usage, errors.New("supply a non-empty dismissal reason with --body or --body-file"))
				}
			} else if suppliedBody != nil && strings.TrimSpace(body) == "" && event == "REQUEST_CHANGES" {
				return clierr.Wrap(clierr.Usage, errors.New("review body cannot be empty for REQUEST_CHANGES"))
			}
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		service := services.Reviews{Client: target.client, Repository: target.Repo}
		if useEditor {
			initial := ""
			if verb == "submit" {
				current, err := service.View(cmd.Context(), n, id)
				if err != nil {
					return err
				}
				initial = current.Body
			}
			draft, err = a.editDraft(cmd, initial)
			if err != nil {
				return err
			}
			body = draft.Body
			suppliedBody = &body
			if strings.TrimSpace(body) == "" && (verb == "dismiss" || event == "REQUEST_CHANGES") {
				return clierr.Wrap(clierr.Usage, errors.New("supply non-empty review text in the editor"))
			}
		}
		if verb == "delete" || verb == "dismiss" {
			message := fmt.Sprintf("%s review %d on %s#%d", verb, id, target.Repo, n)
			if verb == "dismiss" {
				message += ": " + body
			}
			if err := a.confirm(cmd, message); err != nil {
				return err
			}
		}
		if verb == "submit" && event == "REQUEST_CHANGES" && suppliedBody == nil {
			current, err := service.View(cmd.Context(), n, id)
			if err != nil {
				return err
			}
			body = current.Body
			if strings.TrimSpace(body) == "" {
				body = ""
				if _, err := a.require(cmd, &body, "body"); err != nil {
					return err
				}
				if strings.TrimSpace(body) == "" {
					return clierr.Wrap(clierr.Usage, errors.New("review body cannot be empty for REQUEST_CHANGES"))
				}
				if err := a.confirm(cmd, fmt.Sprintf("Submit REQUEST_CHANGES review on %s#%d: %s", target.Repo, n, body)); err != nil {
					return err
				}
			}
			suppliedBody = &body
		}
		if verb == "delete" {
			if err := service.Delete(cmd.Context(), n, id); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Deleted review %d\n", id)
			return err
		}
		var item services.Review
		if verb == "submit" {
			item, err = service.Submit(cmd.Context(), n, id, services.ReviewSubmitOptions{Event: event, Body: suppliedBody})
		} else {
			item, err = service.Dismiss(cmd.Context(), n, id, verb == "dismiss", body)
		}
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, reviewRecord)
	}}
	if verb == "submit" || verb == "dismiss" {
		cmd.Flags().BoolVar(&useEditor, "editor", false, "Edit review text in the configured editor")
		cmd.Flags().StringVar(&body, "body", "", "Review body or dismissal reason (submission preserves draft body when omitted)")
		cmd.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
	}
	if verb == "submit" {
		cmd.Flags().StringVar(&event, "event", "COMMENT", "Review event: APPROVED, REQUEST_CHANGES, COMMENT")
	}
	if verb == "delete" {
		cmd.Annotations = textOutput("review deletion does not return a resource")
	}
	return cmd
}
