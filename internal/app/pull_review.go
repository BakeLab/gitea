// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/terminal"
	"github.com/spf13/cobra"
)

func (a *application) pullReviewCommand() *cobra.Command {
	var event, body, file, commentsFile, commit string
	var useEditor bool
	review := &cobra.Command{Use: "review <number>", Short: "Submit or inspect pull request reviews", Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		n, err := number(args[0])
		if err != nil {
			return err
		}
		snapshot, err := a.deps.Settings.Load()
		if err != nil {
			return err
		}
		policy := a.policy(snapshot)
		if !localFlagsChanged(cmd) && policy.CanPrompt(a.deps.Streams) && a.deps.ReviewForm != nil {
			return a.createReviewInteractively(cmd, n)
		}
		if err := oneOf("event", event, "APPROVED", "REQUEST_CHANGES", "COMMENT", "PENDING"); err != nil {
			return err
		}
		if useEditor {
			draft, err = a.editDraft(cmd, "")
			body = draft.Body
		} else {
			body, err = a.readBody(cmd, body, file)
		}
		if err != nil {
			return err
		}
		opts := services.ReviewOptions{Event: event, Body: body, Commit: commit}
		if commentsFile != "" {
			if commentsFile == "-" {
				return clierr.Wrap(clierr.Usage, errors.New("--comments-file requires a file; use --body-file - for stdin"))
			}
			file, err := os.Open(commentsFile)
			if err != nil {
				return err
			}
			defer file.Close()
			decoder := json.NewDecoder(file)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&opts.Comments); err != nil {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid inline comments: %w", err))
			}
			if opts.Comments == nil {
				return clierr.Wrap(clierr.Usage, errors.New("inline comments must be a JSON array"))
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return clierr.Wrap(clierr.Usage, errors.New("inline comments file must contain exactly one JSON array"))
			}
			for _, comment := range opts.Comments {
				if err := comment.Validate(); err != nil {
					return clierr.Wrap(clierr.Usage, err)
				}
			}
		}
		prompted := false
		if (event == "COMMENT" || event == "REQUEST_CHANGES" || event == "PENDING") && strings.TrimSpace(body) == "" && len(opts.Comments) == 0 {
			if useEditor || cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
				return clierr.Wrap(clierr.Usage, errors.New("review body cannot be empty without inline comments"))
			}
			prompted, err = a.require(cmd, &body, "body")
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("review body cannot be empty"))
			}
			opts.Body = body
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if prompted {
			if err := a.confirm(cmd, fmt.Sprintf("Submit %s review on %s#%d: %s", event, target.Repo, n, body)); err != nil {
				return err
			}
		}
		item, err := (services.Reviews{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), n, opts)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, reviewRecord)
	}}
	review.Flags().StringVar(&event, "event", "COMMENT", "Review event: APPROVED, REQUEST_CHANGES, COMMENT, PENDING")
	review.Flags().StringVar(&body, "body", "", "Review body")
	review.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
	review.Flags().BoolVar(&useEditor, "editor", false, "Compose the review in the configured editor")
	review.Flags().StringVar(&commentsFile, "comments-file", "", "JSON array of path, body, old_position/new_position inline comments")
	review.Flags().StringVar(&commit, "commit", "", "Commit to review (default: current PR head)")
	for _, verb := range []string{"list", "view", "comments"} {
		var limit, page int
		var reviewID int64
		use := verb + " <number>"
		count := 1
		if verb == "view" {
			use += " <review-id>"
			count = 2
		}
		command := &cobra.Command{Use: use, Short: titleCase(verb) + " pull request reviews", Args: argsExact(count), RunE: func(cmd *cobra.Command, args []string) error {
			n, err := number(args[0])
			if err != nil {
				return err
			}
			if reviewID < 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--review must be a positive review ID"))
			}
			if verb == "view" {
				reviewID, err = number(args[1])
				if err != nil {
					return err
				}
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			service := services.Reviews{Client: target.client, Repository: target.Repo}
			switch verb {
			case "view":
				item, err := service.View(cmd.Context(), n, reviewID)
				if err != nil {
					return err
				}
				return writeRecord(a, cmd, item, reviewRecord)
			case "list":
				items, err := service.List(cmd.Context(), n, limit, page)
				if err != nil {
					return err
				}
				return writeRecords(a, cmd, items, reviewRecord)
			case "comments":
				items, err := service.Comments(cmd.Context(), n, reviewID, limit, page)
				if err != nil {
					return err
				}
				return writeList(a, cmd, items, func(item services.ReviewComment) []terminal.Cell {
					return terminal.Row(strconv.FormatInt(item.ID, 10), fmt.Sprintf("%s:%d", item.Path, item.Line), item.Author, item.Body)
				})
			}
			return nil
		}}
		if verb != "view" {
			pageFlags(command, &limit, &page)
		}
		if verb == "comments" {
			command.Flags().Int64Var(&reviewID, "review", 0, "Limit comments to one review ID")
		}
		review.AddCommand(command)
	}
	for _, verb := range []string{"resolve", "unresolve"} {
		review.AddCommand(&cobra.Command{Use: verb + " <comment-id>", Short: titleCase(verb) + " an inline review conversation", Annotations: textOutput("conversation state changes do not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
			id, err := number(args[0])
			if err != nil {
				return err
			}
			target, err := a.resolve(cmd.Context(), true)
			if err != nil {
				return err
			}
			if err := (services.Reviews{Client: target.client, Repository: target.Repo}).Resolve(cmd.Context(), id, verb == "resolve"); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Comment %d %sd\n", id, verb)
			return err
		}})
	}
	review.AddCommand(a.pullReviewReplyCommand())
	review.AddCommand(a.pullReviewRequestCommand(true), a.pullReviewRequestCommand(false))
	for _, verb := range []string{"submit", "delete", "dismiss", "undismiss"} {
		review.AddCommand(a.pullReviewLifecycleCommand(verb))
	}
	return review
}

func (a *application) pullReviewReplyCommand() *cobra.Command {
	var body, file string
	var useEditor bool
	cmd := &cobra.Command{Use: "reply <number> <comment-id>", Short: "Reply to an inline review comment", Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
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
		if useEditor {
			draft, err = a.editDraft(cmd, "")
			body = draft.Body
		} else {
			body, err = a.readBody(cmd, body, file)
		}
		if err != nil {
			return err
		}
		prompted := false
		if !useEditor && !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
			prompted, err = a.require(cmd, &body, "body")
			if err != nil {
				return err
			}
		}
		if strings.TrimSpace(body) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("reply body cannot be empty"))
		}
		target, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if prompted {
			if err := a.confirm(cmd, fmt.Sprintf("Reply to review comment %d on %s#%d: %s", id, target.Repo, n, body)); err != nil {
				return err
			}
		}
		item, err := (services.Reviews{Client: target.client, Repository: target.Repo}).Reply(cmd.Context(), n, id, body)
		if err != nil {
			return err
		}
		return writeRecord(a, cmd, item, func(item services.ReviewComment) terminal.Record {
			return terminal.Record{Heading: terminal.Row(strconv.FormatInt(item.ID, 10)), Lines: []string{item.Body, item.URL}}
		})
	}}
	cmd.Flags().BoolVar(&useEditor, "editor", false, "Write the reply in the configured editor")
	cmd.Flags().StringVar(&body, "body", "", "Reply body")
	cmd.Flags().StringVar(&file, "body-file", "", "Read body from file, or - for stdin")
	return cmd
}

func reviewRecord(item services.Review) terminal.Record {
	return terminal.Record{Heading: terminal.Row(strconv.FormatInt(item.ID, 10), item.State, item.Author), Lines: []string{item.Body, item.URL}}
}

func (a *application) createReviewInteractively(cmd *cobra.Command, n int64) (runErr error) {
	target, err := a.resolve(cmd.Context(), true)
	if err != nil {
		return err
	}
	values, err := a.deps.ReviewForm.CreateReview(cmd.Context(), terminal.ReviewFormChoices{
		ReviewDiff: func(context.Context) ([]terminal.ReviewCommentDraft, error) {
			return a.collectDiffReviewComments(cmd, target, n)
		},
		EditBody: a.bodyEditor(cmd),
	})
	defer finishDraft(&values.Draft, &runErr)
	if err != nil {
		return err
	}
	opts := services.ReviewOptions{Event: values.Event, Body: values.Body}
	for _, comment := range values.Comments {
		opts.Comments = append(opts.Comments, services.InlineComment(comment))
	}
	description := fmt.Sprintf("Submit %s review on %s#%d", values.Event, target.Repo, n)
	if values.Body != "" {
		description += ": " + values.Body
	}
	if err := a.confirm(cmd, description); err != nil {
		return err
	}
	item, err := (services.Reviews{Client: target.client, Repository: target.Repo}).Create(cmd.Context(), n, opts)
	if err != nil {
		return err
	}
	return writeRecord(a, cmd, item, reviewRecord)
}

func (a *application) collectDiffReviewComments(cmd *cobra.Command, t target, n int64) ([]terminal.ReviewCommentDraft, error) {
	diff, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Diff(cmd.Context(), n)
	if err != nil {
		return nil, err
	}
	defer diff.Close()
	path, err := saveDiffReview("", n, t.Repo, diff)
	if err != nil {
		return nil, err
	}
	comments, err := a.reviewDiffFile(cmd, path)
	if err != nil {
		return nil, fmt.Errorf("%w; the annotated diff is kept at %s", err, path)
	}
	_ = os.Remove(path)
	return comments, nil
}

// reviewDiffFile lets the user annotate the diff at path and returns the validated comments.
func (a *application) reviewDiffFile(cmd *cobra.Command, path string) ([]terminal.ReviewCommentDraft, error) {
	if err := a.editPath(cmd, path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("couldn't load diff: %w", err)
	}
	defer file.Close()
	comments, err := parseDiffComments(file)
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		if err := services.InlineComment(comment).Validate(); err != nil {
			return nil, err
		}
	}
	return comments, nil
}
