// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/spf13/cobra"
)

const pullArgumentFormats = `A pull request can be supplied as argument in any of the following formats:
- by number, such as "13" or "#13"
- by URL, such as "https://gitea.com/OWNER/REPO/pulls/13"
- by the name of its head branch, such as "feature/login" or "bob:feature/login"`

const pullArguments = pullArgumentFormats + `

Without an argument, the pull request of the current branch is selected.`

// pullArgumentsAnnotation documents a pull request argument that may be left out.
func pullArgumentsAnnotation(annotations map[string]string) map[string]string {
	return argumentsAnnotation(annotations, pullArguments)
}

// pullArgumentRequiredAnnotation documents a pull request argument that has no default.
func pullArgumentRequiredAnnotation(annotations map[string]string) map[string]string {
	return argumentsAnnotation(annotations, pullArgumentFormats)
}

func argumentsAnnotation(annotations map[string]string, text string) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["gt.arguments"] = text
	return annotations
}

type reviewType struct {
	approve, requestChanges, comment, pending bool
}

// register adds -a, -r, -c, --pending when pending is set, to cmd.
func (r *reviewType) register(cmd *cobra.Command, pending bool) {
	flags := cmd.Flags()
	flags.BoolVarP(&r.approve, "approve", "a", false, "Approve the pull request")
	flags.BoolVarP(&r.requestChanges, "request-changes", "r", false, "Request changes (requires a body)")
	flags.BoolVarP(&r.comment, "comment", "c", false, "Comment without approving (requires a body or inline comments, which can be those of the pending review it publishes; an empty --body clears that review's body when it has inline comments, and is refused otherwise)")
	if pending {
		flags.BoolVar(&r.pending, "pending", false, "Save the review as pending, to submit later with gt pr review submit")
	}
}

// state returns the API state of the chosen review type, or "" when none was chosen.
func (r reviewType) state() (string, error) {
	var states []string
	for state, chosen := range map[string]bool{"APPROVED": r.approve, "REQUEST_CHANGES": r.requestChanges, "COMMENT": r.comment, "PENDING": r.pending} {
		if chosen {
			states = append(states, state)
		}
	}
	switch len(states) {
	case 0:
		return "", nil
	case 1:
		return states[0], nil
	}
	return "", clierr.Wrap(clierr.Usage, errors.New("choose one review type: --approve, --request-changes or --comment"))
}

// errReviewType asks for a review type where gt cannot prompt for one.
func errReviewType(cmd *cobra.Command) error {
	return clierr.Wrap(clierr.Usage, &argumentError{problem: "choose a review type: --approve, --request-changes, or --comment", cmd: cmd})
}

// reviewVerbs reports a submitted review by its state: "Approved pull request alice/x#1 (Add one)".
var reviewVerbs = map[string]string{
	"APPROVED": "Approved", "REQUEST_CHANGES": "Requested changes on", "COMMENT": "Commented on", "PENDING": "Saved pending review on",
}

// reviewNouns name a review by its state in prompts: "Submit approval for #1?".
var reviewNouns = map[string]string{
	"APPROVED": "approval", "REQUEST_CHANGES": "change request", "COMMENT": "comment", "PENDING": "pending review",
}

// reviewWords are review types people try as a subcommand, with the flag that chooses them.
var reviewWords = map[string]string{
	"approve": "approve", "lgtm": "approve", "comment": "comment", "reject": "request-changes", "request-changes": "request-changes",
}

// reviewArgs accepts one pull request and points review types given as a subcommand, as in
// "gt pr review approve 13", at their flags.
func reviewArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if flag, ok := reviewWords[strings.ToLower(args[0])]; ok {
		pull := "<number>"
		if len(args) > 1 {
			pull = args[1]
		}
		return clierr.Wrap(clierr.Usage, fmt.Errorf("unknown review command %q; to %s run: gt pr review %s --%s (see gt pr review --help)",
			args[0], strings.ReplaceAll(flag, "-", " "), pull, flag))
	}
	if len(args) > 1 {
		if _, err := parseRef(args[0], pullRef); errors.Is(err, errNotRef) {
			err := fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
				err = fmt.Errorf("%w; did you mean %s?", err, strings.Join(suggestions, " or "))
			}
			return clierr.Wrap(clierr.Usage, err)
		}
	}
	return usageArgs(cobra.MaximumNArgs(1))(cmd, args)
}

const reviewExamples = `# Approve the pull request of the current branch
$ gt pr review --approve

# Request changes on #13
$ gt pr review 13 -r -b "Please hash the password"

# Start a pending review with inline comments, then submit it
$ gt pr review 13 --pending --comments-file inline.json
$ gt pr review submit 13 --approve`

func (a *application) pullReviewCommand() *cobra.Command {
	review := a.reviewCreateCommand("review [<number> | <url> | <branch>]", "Review a pull request or manage its reviews")
	review.Long = `Submit a review on a pull request, or manage existing reviews with the commands below.

Choose the review type with --approve, --request-changes or --comment. On a terminal, leaving
the type out opens an interactive review that also offers to annotate the diff in your editor.

Gitea adds a review to your pending review on the pull request, if you have one, which publishes
the pending review's inline comments; gt asks first, and without a terminal needs --yes. A body
you give replaces the pending review's body, which gt warns about; without one, it is kept.`
	review.Example = reviewExamples
	review.SuggestionsMinimumDistance = 2
	create := a.reviewCreateCommand("create [<number> | <url> | <branch>]", `Submit a review (same as "gt pr review")`)
	create.Example = `$ gt pr review create 13 --approve
$ gt pr review create --comment -b "Looks good overall"`
	review.AddCommand(create, a.pullReviewListCommand(), a.pullReviewViewCommand(), a.pullReviewCommentsCommand(), a.pullReviewReplyCommand())
	for _, resolve := range []bool{true, false} {
		review.AddCommand(a.pullReviewResolveCommand(resolve))
	}
	review.AddCommand(a.pullReviewRequestCommand(true), a.pullReviewRequestCommand(false))
	review.AddCommand(a.pullReviewSubmitCommand(), a.pullReviewDeleteCommand(), a.pullReviewDismissCommand(true), a.pullReviewDismissCommand(false))
	return jsonOutput(review, jsonFields[services.Review]())
}

func (a *application) reviewCreateCommand(use, short string) *cobra.Command {
	var kind reviewType
	var body, file, commentsFile, commit string
	var useEditor bool
	cmd := &cobra.Command{Use: use, Short: short, Annotations: pullArgumentsAnnotation(map[string]string{"gt.confirm": "true"}), Args: reviewArgs, RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		state, err := kind.state()
		if err != nil {
			return err
		}
		comments, err := readInlineComments(commentsFile)
		if err != nil {
			return err
		}
		if state == "" {
			if localFlagsChanged(cmd) || !a.canPrompt() || a.deps.ReviewForm == nil {
				return errReviewType(cmd)
			}
			return a.reviewInteractively(cmd, args)
		}
		// A comment needs a body or inline comments, and a change request a body. The inline
		// comments can be those of the pending review that a comment publishes, looked up below.
		needsBody := state == "REQUEST_CHANGES" || (state != "APPROVED" && len(comments) == 0)
		missingBody := clierr.Wrap(clierr.Usage, fmt.Errorf("a %s needs a body; pass --body, --body-file or --editor", reviewNouns[state]))
		explicit := cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")
		if body, err = a.readBody(cmd, body, file); err != nil {
			return err
		}
		noBody := func() bool {
			return needsBody && strings.TrimSpace(body) == "" && (explicit || (!useEditor && !a.canPrompt()))
		}
		// A comment without a body can publish a pending review, looked up below.
		if noBody() && state != "COMMENT" {
			return missingBody
		}
		t, n, err := a.pullSelector(cmd, args)
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
		// The diff of another commit than the head is not at hand, so Gitea checks those comments.
		if len(comments) > 0 && commit == "" {
			if err := checkInlineComments(cmd.Context(), t, n, comments); err != nil {
				return err
			}
		}
		service := services.Reviews{Client: t.client, Repository: t.Repo}
		pending, err := pendingReview(cmd, service, t, n)
		if err != nil {
			return err
		}
		if state == "COMMENT" && pendingContent(pending, explicit) {
			needsBody = false
		}
		if needsBody && state == "COMMENT" && explicit && clearsPendingBody(pending, body) {
			return errClearedPendingBody(cmd, pending.ID, body)
		}
		if noBody() {
			return missingBody
		}
		switch {
		case useEditor:
			draft, err = a.editDraft(cmd, "")
		case needsBody && !explicit:
			draft, err = a.bodyEditor(cmd, terminal.BodyTitle)(cmd.Context(), "")
		default:
			draft.Body = body
		}
		if err != nil {
			return err
		}
		if body = draft.Body; strings.TrimSpace(body) == "" {
			dropEmptyDraft(&draft)
			switch {
			case needsBody && useEditor:
				return fmt.Errorf("a %s needs a body, and the editor left it empty", reviewNouns[state])
			case needsBody:
				return missingBody
			}
		}
		if err := a.foldPendingReview(cmd, t, n, state, pending); err != nil {
			return err
		}
		if !explicit {
			// An empty editor draft gives no body either.
			body = pendingBody(pending, body)
		}
		item, err := submitReview(cmd.Context(), service, n, pending, services.ReviewOptions{Event: state, Body: body, Commit: commit, Comments: comments})
		// Gitea fails with an empty server error when the commit to approve or reject is unknown.
		if apiErr, ok := errors.AsType[*transport.APIError](err); ok && commit != "" && apiErr.Status == http.StatusInternalServerError {
			return explain(err, "commit %s not found in pull request #%d", commit, n)
		}
		if err != nil {
			return err
		}
		a.warnReplacedPendingBody(cmd, pending, body)
		return a.reportReview(cmd, t, pull, item)
	}}
	kind.register(cmd, true)
	cmd.Flags().StringVarP(&body, "body", "b", "", "Review body")
	cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Write the body in your editor")
	cmd.Flags().StringVar(&commentsFile, "comments-file", "", "Read inline comments from a JSON `file`: an array of path, body, and new_position or old_position on a line of the diff")
	cmd.Flags().StringVar(&commit, "commit", "", "Commit `sha` to review (default: the head of the pull request)")
	cmd.ValidArgsFunction = firstArg(a.completePulls("open"))
	return cmd
}

// pendingReview returns the account's pending review on pull request n, or nil: Gitea adds a new
// review to the oldest pending one.
func pendingReview(cmd *cobra.Command, service services.Reviews, t target, n int64) (*services.Review, error) {
	pending, err := service.Pending(cmd.Context(), n, t.login())
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	return &pending[0], nil
}

// submitReview creates a review with opts on pull request n. A comment with neither a body nor
// inline comments of its own publishes pending, the pending review it is added to, instead: Gitea
// refuses to create such a comment even when the pending review has inline comments.
func submitReview(ctx context.Context, service services.Reviews, n int64, pending *services.Review, opts services.ReviewOptions) (services.Review, error) {
	if opts.Event == "COMMENT" && pending != nil && strings.TrimSpace(opts.Body) == "" && len(opts.Comments) == 0 {
		return service.Submit(ctx, n, pending.ID, services.ReviewSubmitOptions{Event: opts.Event, Body: &opts.Body})
	}
	return service.Create(ctx, n, opts)
}

// pendingContent reports whether pending, the pending review a comment would publish, has a body
// or inline comments, which the comment then needs no more. The body counts only when it is kept,
// not replaced by an explicit empty body.
func pendingContent(pending *services.Review, replaced bool) bool {
	return pending != nil && ((!replaced && strings.TrimSpace(pending.Body) != "") || pending.CommentsCount > 0)
}

// clearsPendingBody reports whether a comment with body, which publishes pending, the pending
// review it is added to, would leave pending empty: it clears the body of a pending review that
// has no inline comments.
func clearsPendingBody(pending *services.Review, body string) bool {
	return pending != nil && pending.CommentsCount == 0 && strings.TrimSpace(pending.Body) != "" && strings.TrimSpace(body) == ""
}

// dropEmptyDraft removes the editor draft when its body is blank: an empty draft holds nothing to
// keep.
func dropEmptyDraft(draft *terminal.Draft) {
	if strings.TrimSpace(draft.Body) == "" && draft.Path != "" {
		_ = os.Remove(draft.Path)
		draft.Path = ""
	}
}

// errClearedPendingBody refuses body, the empty or blank body given with --body or --body-file, of
// a comment that would publish pending review id with its body cleared and nothing else: Gitea
// refuses an empty review.
func errClearedPendingBody(cmd *cobra.Command, id int64, body string) error {
	given, flag := `-b ""`, "--body"
	switch {
	case cmd.Flags().Changed("body-file") && body == "":
		given, flag = "an empty --body-file", "--body-file"
	case cmd.Flags().Changed("body-file"):
		given, flag = "a blank --body-file", "--body-file"
	case body != "":
		given = "a blank --body"
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("%s would clear the body of pending review %d and leave it empty; leave out %s to keep its body", given, id, flag))
}

// foldPendingReview asks to fold review, the account's pending review on pull request n, if any,
// into the review about to be submitted with state: Gitea adds the new review to it, which
// publishes the pending review's inline comments and replaces its body. A new pending review just
// adds to the pending one, without asking.
func (a *application) foldPendingReview(cmd *cobra.Command, t target, n int64, state string, review *services.Review) error {
	if review == nil || state == "PENDING" {
		return nil
	}
	comments := ""
	switch {
	case review.CommentsCount == 1:
		comments = " and its inline comment"
	case review.CommentsCount > 1:
		comments = fmt.Sprintf(" and its %d inline comments", review.CommentsCount)
	}
	confirmation := terminal.Confirmation{
		Prompt: fmt.Sprintf("Your pending review %d on #%d becomes part of this %s, which publishes it%s. Continue?", review.ID, n, reviewNouns[state], comments),
		NonTTY: fmt.Sprintf("publish your pending review %d on %s#%d%s with this %s (or submit it with gt pr review submit %d)", review.ID, t.Repo, n, comments, reviewNouns[state], n),
	}
	return a.confirmAction(cmd, confirmation)
}

// ownReview refuses to approve or request changes on one's own pull request before any body is
// written, as Gitea would after.
func ownReview(pull services.Pull, t target, state string) error {
	if (state == "APPROVED" || state == "REQUEST_CHANGES") && t.login() != "" && strings.EqualFold(pull.Author, t.login()) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("you can't approve or request changes on your own pull request #%d; use --comment", pull.Number))
	}
	return nil
}

// reportReview reports a submitted review, then prints its URL, or the review as JSON.
func (a *application) reportReview(cmd *cobra.Command, t target, pull services.Pull, item services.Review) error {
	a.done(cmd, "%s pull request %s", reviewVerbs[item.State], refLabel(t.Repo, pull.Number, pull.Title))
	if a.options.json == "" && item.URL == "" {
		return nil
	}
	return a.created(cmd, item, item.URL)
}

func readInlineComments(path string) ([]services.InlineComment, error) {
	if path == "" {
		return nil, nil
	}
	if path == "-" {
		return nil, clierr.Wrap(clierr.Usage, errors.New("--comments-file requires a file; use --body-file - for stdin"))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var comments []services.InlineComment
	if err := decoder.Decode(&comments); err != nil {
		return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid inline comments: %w", err))
	}
	if comments == nil {
		return nil, clierr.Wrap(clierr.Usage, errors.New("inline comments must be a JSON array"))
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, clierr.Wrap(clierr.Usage, errors.New("inline comments file must contain exactly one JSON array"))
	}
	for _, comment := range comments {
		if err := comment.Validate(); err != nil {
			return nil, clierr.Wrap(clierr.Usage, err)
		}
	}
	return comments, nil
}

// checkInlineComments refuses inline comments that are not on a line of the diff of pull request n,
// which Gitea would store without showing them in the diff.
func checkInlineComments(ctx context.Context, t target, n int64, comments []services.InlineComment) error {
	diff, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Diff(ctx, n)
	if err != nil {
		return err
	}
	defer diff.Close()
	data, err := io.ReadAll(diff)
	if err != nil {
		return err
	}
	return checkDiffComments(string(data), n, comments)
}

// checkDiffComments refuses inline comments that are not on a line of diff, the diff of pull request n.
func checkDiffComments(diff string, n int64, comments []services.InlineComment) error {
	lines := diffLines(diff)
	files := map[string]bool{}
	for line := range lines {
		files[line.path] = true
	}
	for _, comment := range comments {
		side, line := "new_position", comment.NewLine
		if comment.OldLine != 0 {
			side, line = "old_position", comment.OldLine
		}
		switch {
		case !files[comment.Path]:
			return clierr.Wrap(clierr.Usage, fmt.Errorf("inline comment on %q: pull request #%d does not change that file", comment.Path, n))
		case !lines[diffLine{comment.Path, comment.OldLine != 0, line}]:
			return clierr.Wrap(clierr.Usage, fmt.Errorf("inline comment on %q: %s %d is not a line of the diff of pull request #%d", comment.Path, side, line, n))
		}
	}
	return nil
}

func (a *application) reviewInteractively(cmd *cobra.Command, args []string) error {
	t, n, err := a.pullSelector(cmd, args)
	if err != nil {
		return err
	}
	pull, err := (services.Pulls{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n)
	if err != nil {
		return err
	}
	preferences, err := a.preferences()
	if err != nil {
		return err
	}
	service := services.Reviews{Client: t.client, Repository: t.Repo}
	pending, err := pendingReview(cmd, service, t, n)
	if err != nil {
		return err
	}
	choices := terminal.ReviewFormChoices{
		Header:  fmt.Sprintf("Reviewing #%d %q (%s → %s) in %s", n, pull.Title, pullHead(pull, t.Repo), pull.Base, t.Repo),
		Author:  t.login() != "" && strings.EqualFold(pull.Author, t.login()),
		Editor:  preferences.Editor,
		Pending: pendingContent(pending, false),
	}
	// The annotated diff stays until the review is created, so declining or a failure loses no comment.
	var diffPath string
	if preferences.Editor != "" {
		choices.ReviewDiff = func(context.Context) ([]terminal.ReviewCommentDraft, error) {
			comments, path, err := a.collectDiffReviewComments(cmd, t, n)
			diffPath = path
			return comments, err
		}
	}
	values, err := a.deps.ReviewForm.CreateReview(cmd.Context(), choices)
	if err == nil {
		err = a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Submit %s for #%d?", reviewNouns[values.Event], n), DefaultYes: true})
	}
	var item services.Review
	if err == nil {
		err = a.foldPendingReview(cmd, t, n, values.Event, pending)
	}
	body := values.Body
	if err == nil {
		body = pendingBody(pending, body)
		opts := services.ReviewOptions{Event: values.Event, Body: body}
		for _, comment := range values.Comments {
			opts.Comments = append(opts.Comments, services.InlineComment(comment))
		}
		item, err = submitReview(cmd.Context(), service, n, pending, opts)
	}
	if err != nil {
		draft := keepDraft(values.Body)
		finishDraft(&draft, &err)
		if diffPath != "" {
			err = fmt.Errorf("%w; the annotated diff is kept at %s", err, diffPath)
		}
		return err
	}
	if diffPath != "" {
		_ = os.Remove(diffPath)
	}
	a.warnReplacedPendingBody(cmd, pending, body)
	return a.reportReview(cmd, t, pull, item)
}

// pendingBody returns body, or when it is blank the body of pending, the pending review that the
// new review is added to: Gitea replaces the pending review's body, so keep it when none was
// given, as gt pr review submit does.
func pendingBody(pending *services.Review, body string) string {
	if pending != nil && strings.TrimSpace(body) == "" {
		return pending.Body
	}
	return body
}

// warnReplacedPendingBody warns that body replaced the body of pending, the pending review that
// Gitea added a review to.
func (a *application) warnReplacedPendingBody(cmd *cobra.Command, pending *services.Review, body string) {
	if pending != nil && strings.TrimSpace(pending.Body) != "" && body != pending.Body {
		a.warn(cmd, "Replaced the earlier body of your pending review %d", pending.ID)
	}
}

// pullHead names the head branch of a pull request, as OWNER:BRANCH when it is in a fork of repo.
func pullHead(item services.Pull, repo string) string {
	if owner, _, ok := strings.Cut(item.HeadRepo, "/"); ok && !strings.EqualFold(item.HeadRepo, repo) {
		return owner + ":" + item.Head
	}
	return item.Head
}

// collectDiffReviewComments returns the comments annotated into the diff of pull request n and the
// file holding it, which the caller removes once the review is created.
func (a *application) collectDiffReviewComments(cmd *cobra.Command, t target, n int64) ([]terminal.ReviewCommentDraft, string, error) {
	// The existing comments only help the reviewer, so the review goes on without them.
	var existing []services.ReviewComment
	var loaded sync.WaitGroup
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	loaded.Go(func() {
		reviews := services.Reviews{Client: t.client, Repository: t.Repo}
		if all, err := reviews.All(ctx, n); err == nil {
			existing, _ = reviews.Inline(ctx, n, all)
		}
	})
	diff, err := (services.Pulls{Client: t.client, Repository: t.Repo}).Diff(cmd.Context(), n)
	if err != nil {
		cancel()
		loaded.Wait()
		return nil, "", err
	}
	loaded.Wait()
	data, err := io.ReadAll(diff)
	diff.Close()
	if err != nil {
		return nil, "", err
	}
	path, annotated, err := saveDiffReview("", n, t.Repo, strings.NewReader(string(data)), existing)
	if err != nil {
		return nil, "", err
	}
	comments, err := a.reviewDiffFile(cmd, path, annotated, string(data), n)
	if err != nil {
		return nil, "", fmt.Errorf("%w; the annotated diff is kept at %s", err, path)
	}
	return comments, path, nil
}

// reviewDiffFile lets the user annotate diff, the diff of pull request n saved at path as annotated,
// and returns the comments once they are checked against diff.
func (a *application) reviewDiffFile(cmd *cobra.Command, path, annotated, diff string, n int64) ([]terminal.ReviewCommentDraft, error) {
	if err := a.editPath(cmd, path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("couldn't load diff: %w", err)
	}
	defer file.Close()
	comments, err := parseDiffComments(annotated, file)
	if err != nil {
		return nil, err
	}
	inline := make([]services.InlineComment, 0, len(comments))
	for _, comment := range comments {
		if err := services.InlineComment(comment).Validate(); err != nil {
			return nil, err
		}
		inline = append(inline, services.InlineComment(comment))
	}
	if err := checkDiffComments(diff, n, inline); err != nil {
		return nil, err
	}
	return comments, nil
}

// reviewStateCell words a review's state for people, colored like gh: Approved, Changes requested,
// Commented, Pending or Requested, with "(dismissed)" for dismissed reviews.
func reviewStateCell(item services.Review) terminal.Cell {
	words := map[string]terminal.Cell{
		"APPROVED": {Text: "Approved", Color: "green"}, "REQUEST_CHANGES": {Text: "Changes requested", Color: "red"},
		"COMMENT": {Text: "Commented"}, "PENDING": {Text: "Pending", Color: "yellow"}, "REQUEST_REVIEW": {Text: "Requested", Muted: true},
	}
	cell, ok := words[item.State]
	if !ok {
		cell = terminal.Cell{Text: item.State}
	}
	if item.Dismissed {
		cell = terminal.Cell{Text: cell.Text + " (dismissed)", Muted: true}
	}
	return cell
}

// reviewPossessive names a review by its author and state in feedback: "bob's approval".
func reviewPossessive(item services.Review) string {
	noun := "review"
	switch item.State {
	case "APPROVED":
		noun = "approval"
	case "REQUEST_CHANGES":
		noun = "change request"
	}
	return item.Reviewer() + "'s " + noun
}

// pullAndID resolves the pull request and the ID that review subcommands take as their two arguments.
func (a *application) pullAndID(cmd *cobra.Command, args []string) (target, int64, int64, error) {
	id, err := number(args[1])
	if err != nil {
		return target{}, 0, 0, err
	}
	t, n, err := a.pullSelector(cmd, args[:1])
	return t, n, id, err
}

func (a *application) pullReviewListCommand() *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{Use: "list [<number> | <url> | <branch>]", Short: "List reviews on a pull request", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		window, err := (services.Reviews{Client: t.client, Repository: t.Repo}).ListPage(cmd.Context(), n, limit, page)
		if err != nil {
			return err
		}
		ref := fmt.Sprintf("%s#%d", t.Repo, n)
		return writeTable(a, cmd, window.Items, listSpec[services.Review]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Reviewer"}, {Header: "State"}, {Header: "Submitted"}, {Header: "Comments"}, {Header: "Body", Flex: true}},
			Row: func(item services.Review) []terminal.Cell {
				return []terminal.Cell{
					{Text: strconv.FormatInt(item.ID, 10), Link: item.URL},
					{Text: item.Reviewer()},
					reviewStateCell(item),
					{Time: terminal.ParseTime(item.SubmittedAt)},
					{Text: strconv.Itoa(item.CommentsCount)},
					{Text: item.Body},
				}
			},
			Empty: "no reviews on " + ref, Context: "reviews on " + ref,
		}, servicePage(window, page))
	}}
	pageFlags(cmd, &limit, &page)
	cmd.Example = `$ gt pr review list 13
$ gt pr review list --json id,author,state`
	cmd.ValidArgsFunction = firstArg(a.completePulls("all"))
	return cmd
}

func (a *application) pullReviewViewCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "view <number> <review-id>", Short: "View a review", Annotations: pullArgumentRequiredAnnotation(nil), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) error {
		t, n, id, err := a.pullAndID(cmd, args)
		if err != nil {
			return err
		}
		item, err := (services.Reviews{Client: t.client, Repository: t.Repo}).View(cmd.Context(), n, id)
		if err != nil {
			return err
		}
		now := a.deps.Now()
		return writeView(a, cmd, item, func(item services.Review) terminal.View {
			meta := item.Reviewer()
			if submitted := terminal.ParseTime(item.SubmittedAt); !submitted.IsZero() {
				meta += " • " + terminal.RelativeTime(now, submitted)
			}
			return terminal.View{
				Title: fmt.Sprintf("Review on %s#%d", t.Repo, n), ID: strconv.FormatInt(item.ID, 10), Badges: []terminal.Cell{reviewStateCell(item)}, Meta: meta,
				Fields: []terminal.Field{
					{Label: "Reviewer", Value: item.Reviewer(), PipedOnly: true},
					{Label: "State", Value: item.State, PipedOnly: true},
					{Label: "Inline comments", Value: strconv.Itoa(item.CommentsCount)},
					{Label: "Commit", Value: item.Commit},
				},
				Body: item.Body, Kind: "review", URL: item.URL,
			}
		})
	}}
	cmd.Example = "$ gt pr review view 13 6"
	cmd.ValidArgsFunction = a.completeReviewArgs(false)
	return cmd
}

func (a *application) pullReviewCommentsCommand() *cobra.Command {
	var limit, page int
	var reviewID int64
	cmd := &cobra.Command{Use: "comments [<number> | <url> | <branch>]", Short: "List inline review comments", Annotations: pullArgumentsAnnotation(nil), Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if reviewID < 0 {
			return clierr.Wrap(clierr.Usage, errors.New("--review must be a positive review ID"))
		}
		t, n, err := a.pullSelector(cmd, args)
		if err != nil {
			return err
		}
		result, err := (services.Reviews{Client: t.client, Repository: t.Repo}).Comments(cmd.Context(), n, reviewID, limit, page)
		if err != nil {
			return err
		}
		ref := fmt.Sprintf("%s#%d", t.Repo, n)
		return writeTable(a, cmd, result.Items, listSpec[services.ReviewComment]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "File"}, {Header: "Author"}, {Header: "Resolved"}, {Header: "Body", Flex: true}},
			Row: func(item services.ReviewComment) []terminal.Cell {
				return []terminal.Cell{{Text: strconv.FormatInt(item.ID, 10), Link: item.URL}, {Text: item.Location()}, {Text: item.Author}, {Text: item.Resolver}, {Text: item.Body}}
			},
			Empty: "no inline review comments on " + ref, Context: "inline review comments on " + ref,
		}, servicePage(result, page))
	}}
	pageFlags(cmd, &limit, &page)
	cmd.Flags().Int64Var(&reviewID, "review", 0, "Limit comments to one review `ID`")
	cmd.Example = `$ gt pr review comments 13
$ gt pr review comments 13 --review 6`
	cmd.ValidArgsFunction = firstArg(a.completePulls("all"))
	return jsonOutput(cmd, jsonFields[services.ReviewComment]())
}

func (a *application) pullReviewResolveCommand(resolve bool) *cobra.Command {
	verb, short, done := "resolve", "Mark an inline conversation as resolved", "Resolved"
	if !resolve {
		verb, short, done = "unresolve", "Reopen a resolved inline conversation", "Reopened"
	}
	cmd := &cobra.Command{Use: verb + " <comment-id>", Short: short, Annotations: textOutput("conversation state changes do not return a resource"), Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, err := number(args[0])
		if err != nil {
			return err
		}
		t, err := a.resolve(cmd.Context(), true)
		if err != nil {
			return err
		}
		if err := (services.Reviews{Client: t.client, Repository: t.Repo}).Resolve(cmd.Context(), id, resolve); err != nil {
			return err
		}
		a.done(cmd, "%s conversation of review comment %d in %s", done, id, t.Repo)
		return nil
	}}
	cmd.Example = fmt.Sprintf("# Find the comment IDs with: gt pr review comments 13\n$ gt pr review %s 36", verb)
	return cmd
}

func (a *application) pullReviewReplyCommand() *cobra.Command {
	var body, file string
	var useEditor bool
	cmd := &cobra.Command{Use: "reply <number> <comment-id>", Short: "Reply to an inline review comment", Annotations: pullArgumentRequiredAnnotation(nil), Args: argsExact(2), RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		var draft terminal.Draft
		defer finishDraft(&draft, &runErr)
		if err := validateBodySources(cmd); err != nil {
			return err
		}
		t, n, id, err := a.pullAndID(cmd, args)
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
		if !useEditor && !cmd.Flags().Changed("body") && !cmd.Flags().Changed("body-file") {
			if !a.canPrompt() {
				return clierr.Wrap(clierr.Usage, errors.New("a reply needs a body; pass --body, --body-file or --editor"))
			}
			if draft, err = a.bodyEditor(cmd, terminal.BodyTitle)(cmd.Context(), ""); err != nil {
				return err
			}
			body = draft.Body
		}
		if strings.TrimSpace(body) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("reply body cannot be empty"))
		}
		item, err := (services.Reviews{Client: t.client, Repository: t.Repo}).Reply(cmd.Context(), n, id, body)
		if err != nil {
			return err
		}
		a.done(cmd, "Replied to review comment %d on %s#%d", id, t.Repo, n)
		return a.created(cmd, item, item.URL)
	}}
	cmd.Flags().StringVarP(&body, "body", "b", "", "Reply body")
	cmd.Flags().StringVarP(&file, "body-file", "F", "", "Read the body from `file`, or - for stdin")
	cmd.Flags().BoolVarP(&useEditor, "editor", "e", false, "Write the reply in your editor")
	cmd.Example = `$ gt pr review reply 13 36 -b "Done, the password is hashed now"`
	cmd.ValidArgsFunction = a.completeReviewArgs(true)
	return jsonOutput(cmd, jsonFields[services.ReviewComment]())
}

// completeReviewArgs completes an open pull request, then the IDs of its reviews, or of its inline
// review comments when inline is set.
func (a *application) completeReviewArgs(inline bool) cobra.CompletionFunc {
	pulls := a.completePulls("open")
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return pulls(cmd, args, toComplete)
		}
		if len(args) > 1 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		n, err := number(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return a.apiCompletion(true, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
			service := services.Reviews{Client: t.client, Repository: t.Repo}
			if inline {
				items, err := service.Comments(ctx, n, 0, completionLimit, 1)
				return describe(items.Items, func(item services.ReviewComment) (string, string) {
					return strconv.FormatInt(item.ID, 10), item.Author + " on " + item.Path + ": " + item.Body
				}), err
			}
			items, err := service.List(ctx, n, completionLimit, 1)
			return describe(items, func(item services.Review) (string, string) {
				return strconv.FormatInt(item.ID, 10), reviewStateCell(item).Text + " by " + item.Reviewer()
			}), err
		})(cmd, args, toComplete)
	}
}
