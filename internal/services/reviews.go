// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Review is a stable review result.
type Review struct {
	ID            int64  `json:"id"`
	Body          string `json:"body"`
	State         string `json:"state"`
	Author        string `json:"author"`
	Team          string `json:"team"`
	URL           string `json:"url"`
	Commit        string `json:"commit"`
	SubmittedAt   string `json:"submitted_at"`
	CommentsCount int    `json:"comments_count"`
	Dismissed     bool   `json:"dismissed"`
	// Stale tells whether the pull request changed since the review.
	Stale bool `json:"stale"`
	// Official tells whether the review counts towards the approvals the base branch requires.
	Official bool `json:"official"`
}

// Reviewer names who the review is by, or for a request to a team, "team NAME".
func (r Review) Reviewer() string {
	if r.Author == "" && r.Team != "" {
		return "team " + r.Team
	}
	return r.Author
}

func reviewFrom(review *gitea.PullReview) Review {
	team := ""
	if review.ReviewerTeam != nil {
		team = review.ReviewerTeam.Name
	}
	return Review{
		ID: review.ID, Body: review.Body, State: string(review.State), Author: login(review.Reviewer), Team: team, URL: review.HTMLURL,
		Commit: review.CommitID, SubmittedAt: timestamp(review.Submitted), CommentsCount: review.CodeCommentsCount, Dismissed: review.Dismissed,
		Stale: review.Stale, Official: review.Official,
	}
}

// ReviewComment describes an inline review conversation.
type ReviewComment struct {
	ID        int64  `json:"id"`
	ReviewID  int64  `json:"review_id"`
	Body      string `json:"body"`
	Author    string `json:"author"`
	Resolver  string `json:"resolver"`
	URL       string `json:"url"`
	Path      string `json:"path"`
	Line      int64  `json:"line"`
	OldLine   int64  `json:"old_line"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func reviewCommentFrom(comment *gitea.PullReviewComment) ReviewComment {
	return ReviewComment{
		ID: comment.ID, ReviewID: comment.ReviewID, Body: comment.Body, Author: login(comment.Reviewer), Resolver: login(comment.Resolver),
		URL: comment.HTMLURL, Path: comment.Path, Line: int64(comment.LineNum), OldLine: int64(comment.OldLineNum), CreatedAt: timestamp(comment.Created), UpdatedAt: timestamp(comment.Updated),
	}
}

// Location names the file and line a comment is on: "app.go:8", or "app.go:4 (old)" for a
// removed line, which has only an old position.
func (c ReviewComment) Location() string {
	switch {
	case c.Line > 0:
		return fmt.Sprintf("%s:%d", c.Path, c.Line)
	case c.OldLine > 0:
		return fmt.Sprintf("%s:%d (old)", c.Path, c.OldLine)
	}
	return c.Path
}

// InlineComment supplies an explicit file position for review creation.
type InlineComment struct {
	Path    string `json:"path"`
	Body    string `json:"body"`
	OldLine int64  `json:"old_position"`
	NewLine int64  `json:"new_position"`
}

// Validate requires a path, a body, and exactly one positive line position.
func (c InlineComment) Validate() error {
	if strings.TrimSpace(c.Path) == "" || strings.TrimSpace(c.Body) == "" || c.OldLine < 0 || c.NewLine < 0 || (c.OldLine == 0) == (c.NewLine == 0) {
		return errors.New("each inline comment requires path, body, and exactly one positive old_position or new_position")
	}
	return nil
}

// ReviewOptions supplies collected review text and inline comments.
type ReviewOptions struct {
	Event    string          `json:"event"`
	Body     string          `json:"body"`
	Commit   string          `json:"commit_id,omitempty"`
	Comments []InlineComment `json:"comments,omitempty"`
}

// ReviewSubmitOptions preserves existing draft text when Body is omitted.
type ReviewSubmitOptions struct {
	Event string
	Body  *string
}

// Reviews manages pull request reviews.
type Reviews struct {
	Client     *transport.Client
	Repository string
}

// List returns a non-nil page of reviews.
func (s Reviews) List(ctx context.Context, n int64, limit, page int) ([]Review, error) {
	reviews, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.ListPullReviews(ctx, owner, name, n, gitea.ListPullReviewsOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return nil, repositoryItemError(ctx, s.Client, s.Repository, err, "pull request", "#"+strconv.FormatInt(n, 10))
	}
	return convertAll(reviews, reviewFrom), nil
}

// ListPage lists a window of reviews of pull request n. It reaches past the server's smaller
// page size, which List would silently take as the whole window.
func (s Reviews) ListPage(ctx context.Context, n int64, limit, page int) (Page[Review], error) {
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.PullReview, http.Header, error) {
		var headers http.Header
		reviews, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.PullReview, *gitea.Response, error) {
			reviews, resp, err := c.PullRequests.ListPullReviews(ctx, owner, name, n, gitea.ListPullReviewsOptions{ListOptions: listOptions(limit, page)})
			headers = headerOf(resp)
			return reviews, resp, err
		})
		return reviews, headers, err
	})
	if err != nil {
		return Page[Review]{}, repositoryItemError(ctx, s.Client, s.Repository, err, "pull request", "#"+strconv.FormatInt(n, 10))
	}
	return convertPage(window, reviewFrom), nil
}

// Pending returns the reviews that login started on pull request n and has not submitted yet.
func (s Reviews) Pending(ctx context.Context, n int64, login string) ([]Review, error) {
	reviews, err := s.All(ctx, n)
	if err != nil {
		return nil, err
	}
	pending := []Review{}
	for _, review := range reviews {
		if review.State == string(gitea.ReviewStatePending) && strings.EqualFold(review.Author, login) {
			pending = append(pending, review)
		}
	}
	return pending, nil
}

// View retrieves a review belonging to the selected pull request.
func (s Reviews) View(ctx context.Context, n, id int64) (Review, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.GetPullReview(ctx, owner, name, n, id)
	})
	return item, s.reviewError(ctx, err, n, id)
}

// reviewError names the missing repository, pull request or review of a 404. Gitea names a missing
// pull request or review in its message, but answers a missing repository and a review of another
// pull request with the same bare "not found", so the repository is checked then.
func (s Reviews) reviewError(ctx context.Context, err error, n, id int64) error {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok || apiErr.Status != http.StatusNotFound {
		return err
	}
	if strings.HasPrefix(apiErr.Message, "pull request does not exist") {
		return &NotFoundError{Kind: "pull request", Key: "#" + strconv.FormatInt(n, 10), Scope: s.Repository, Err: err}
	}
	missing := &NotFoundError{Kind: "review", Key: strconv.FormatInt(id, 10), Scope: "pull request #" + strconv.FormatInt(n, 10), Err: err}
	if strings.HasPrefix(apiErr.Message, "review does not exist") {
		return missing
	}
	if repoErr := repositoryItemError(ctx, s.Client, s.Repository, err, "review", missing.Key); isMissingRepository(repoErr) {
		return repoErr
	}
	return missing
}

// isMissingRepository reports whether err is the error of a missing repository.
func isMissingRepository(err error) bool {
	missing, ok := errors.AsType[*NotFoundError](err)
	return ok && missing.Kind == "repository"
}

// Create submits a review.
func (s Reviews) Create(ctx context.Context, n int64, opts ReviewOptions) (Review, error) {
	option := gitea.CreatePullReviewOptions{State: gitea.ReviewStateType(opts.Event), Body: opts.Body, CommitID: opts.Commit}
	for _, comment := range opts.Comments {
		option.Comments = append(option.Comments, gitea.CreatePullReviewComment{
			Path: comment.Path, Body: comment.Body, OldLineNum: comment.OldLine, NewLineNum: comment.NewLine,
		})
	}
	item, err := repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.CreatePullReview(ctx, owner, name, n, option)
	})
	return item, s.closedPullError(ctx, n, Classify(err, "pull request", "#"+strconv.FormatInt(n, 10), s.Repository), "only comment reviews can be submitted")
}

// closedPullError tells whether pull request n is merged or closed when Gitea refuses a review
// change with its bare "closed or merged PR" 422, or 403 for a dismissal, and returns err otherwise.
func (s Reviews) closedPullError(ctx context.Context, n int64, err error, hint string) error {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok || (apiErr.Status != http.StatusUnprocessableEntity && apiErr.Status != http.StatusForbidden) || !strings.Contains(apiErr.Message, "closed or merged PR") {
		return err
	}
	pull, pullErr := (Pulls{Client: s.Client, Repository: s.Repository}).get(ctx, n)
	if pullErr != nil {
		return err
	}
	conflict := &ConflictError{Reason: ReasonClosed, Kind: "pull request", Key: "#" + strconv.FormatInt(n, 10), Hint: hint, Err: err}
	if pull.HasMerged {
		conflict.Reason = ReasonMerged
	}
	return conflict
}

// Submit publishes an existing pending review with explicitly collected input.
func (s Reviews) Submit(ctx context.Context, n, id int64, opts ReviewSubmitOptions) (Review, error) {
	var body string
	if opts.Body == nil {
		current, err := s.View(ctx, n, id)
		if err != nil {
			return Review{}, err
		}
		body = current.Body
	} else {
		body = *opts.Body
	}
	option := gitea.SubmitPullReviewOptions{State: gitea.ReviewStateType(opts.Event), Body: body}
	if option.State != gitea.ReviewStateApproved && strings.TrimSpace(body) == "" {
		// TODO(sdk): SubmitPullReview requires a body unless the event is APPROVED, but Gitea accepts
		// an empty body when the pending review has inline comments.
		var review gitea.PullReview
		endpoint := fmt.Sprintf("repos/%s/pulls/%d/reviews/%d", s.Repository, n, id)
		if err := sendJSON(ctx, s.Client, http.MethodPost, endpoint, option, &review); err != nil {
			return Review{}, s.closedPullError(ctx, n, err, "only comment reviews can be submitted")
		}
		return reviewFrom(&review), nil
	}
	item, err := repoConvert(ctx, s.Client, s.Repository, reviewFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReview, *gitea.Response, error) {
		return c.PullRequests.SubmitPullReview(ctx, owner, name, n, id, option)
	})
	return item, s.closedPullError(ctx, n, s.reviewError(ctx, err, n, id), "only comment reviews can be submitted")
}

// Delete removes one review.
func (s Reviews) Delete(ctx context.Context, n, id int64) error {
	return s.reviewError(ctx, repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		return c.PullRequests.DeletePullReview(ctx, owner, name, n, id)
	}), n, id)
}

// Dismiss changes a review's contribution to approval requirements and returns the review.
func (s Reviews) Dismiss(ctx context.Context, n, id int64, dismiss bool, message string) (Review, error) {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if dismiss {
			return c.PullRequests.DismissPullReview(ctx, owner, name, n, id, gitea.DismissPullReviewOptions{Message: message})
		}
		return c.PullRequests.UnDismissPullReview(ctx, owner, name, n, id)
	})
	if err != nil {
		return Review{}, s.closedPullError(ctx, n, s.reviewError(ctx, err, n, id), "")
	}
	review, err := s.View(ctx, n, id)
	if err != nil {
		return Review{}, fmt.Errorf("the review was updated, but reading it failed: %w", err)
	}
	return review, nil
}

// Comments flattens the API's unpaginated per-review collections in ID order.
func (s Reviews) Comments(ctx context.Context, n, reviewID int64, limit, page int) (Page[ReviewComment], error) {
	reviews := []Review{{ID: reviewID}}
	if reviewID == 0 {
		var err error
		reviews, err = s.All(ctx, n)
		if err != nil {
			return Page[ReviewComment]{}, err
		}
	}
	results, err := s.reviewComments(ctx, n, reviews, reviewID == 0)
	if err != nil {
		return Page[ReviewComment]{}, err
	}
	return PageOf(results, limit, page)
}

// All returns every review of a pull request, including requests for a review.
func (s Reviews) All(ctx context.Context, n int64) ([]Review, error) {
	return listEvery(100, func(limit, page int) (Page[Review], error) { return s.ListPage(ctx, n, limit, page) })
}

// Inline returns the inline comments of the given reviews of pull request n.
func (s Reviews) Inline(ctx context.Context, n int64, reviews []Review) ([]ReviewComment, error) {
	return s.reviewComments(ctx, n, reviews, true)
}

// LatestReviews returns each reviewer's latest review, in the order of their first review. Pending
// reviews, which only their author sees, are left out; a later request for a review replaces a review.
func LatestReviews(reviews []Review) []Review {
	reviews = slices.Clone(reviews)
	slices.SortStableFunc(reviews, func(x, y Review) int { return cmp.Compare(x.ID, y.ID) })
	at := map[string]int{}
	var latest []Review
	for _, review := range reviews {
		if review.Author == "" || review.State == string(gitea.ReviewStatePending) {
			continue
		}
		if i, ok := at[review.Author]; ok {
			latest[i] = review
			continue
		}
		at[review.Author] = len(latest)
		latest = append(latest, review)
	}
	return latest
}

// RequestedReviewers returns the users and teams with an outstanding review request: each user
// whose latest review is a request, and each team with a request. Gitea's requested_reviewers
// also lists users who reviewed, even after their request was canceled, so the reviews decide.
func RequestedReviewers(reviews []Review) (users, teams []string) {
	users, teams = []string{}, []string{}
	for _, review := range LatestReviews(reviews) {
		if review.State == string(gitea.ReviewStateRequestReview) {
			users = append(users, review.Author)
		}
	}
	for _, review := range reviews {
		if review.Author == "" && review.Team != "" && review.State == string(gitea.ReviewStateRequestReview) && !slices.Contains(teams, review.Team) {
			teams = append(teams, review.Team)
		}
	}
	return users, teams
}

// ReviewDecision summarizes reviews like gh: changes_requested when a reviewer's latest decisive
// review requests changes, else approved when one approves, else review_required while reviews
// are requested, else "". Comment reviews are not decisive (as in gh and Gitea) and dismissed
// reviews do not count.
func ReviewDecision(reviews []Review, requested bool) string {
	decisive := make([]Review, 0, len(reviews))
	for _, review := range reviews {
		if review.State != string(gitea.ReviewStateComment) {
			decisive = append(decisive, review)
		}
	}
	decision := ""
	for _, review := range LatestReviews(decisive) {
		switch {
		case review.Dismissed:
		case review.State == string(gitea.ReviewStateRequestChanges):
			return "changes_requested"
		case review.State == string(gitea.ReviewStateApproved):
			decision = "approved"
		case review.State == string(gitea.ReviewStateRequestReview):
			requested = true
		}
	}
	if decision == "" && requested {
		return "review_required"
	}
	return decision
}

// OfficialApprovals counts the reviewers whose latest decisive review is an official approval
// that was not dismissed, nor stale with skipStale.
func OfficialApprovals(reviews []Review, skipStale bool) int64 {
	var approvals int64
	for _, review := range LatestReviews(slices.DeleteFunc(slices.Clone(reviews), func(review Review) bool { return review.State == string(gitea.ReviewStateComment) })) {
		if review.State == string(gitea.ReviewStateApproved) && review.Official && !review.Dismissed && !(skipStale && review.Stale) {
			approvals++
		}
	}
	return approvals
}

func (s Reviews) reviewComments(ctx context.Context, n int64, reviews []Review, skipEmpty bool) ([]ReviewComment, error) {
	results := []ReviewComment{}
	for _, review := range reviews {
		// Pending reviews are drafts that only their author and admins see; an explicit review ID still lists them.
		if skipEmpty && (review.CommentsCount == 0 || review.State == string(gitea.ReviewStatePending)) {
			continue
		}
		comments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.PullReviewComment, *gitea.Response, error) {
			return c.PullRequests.ListPullReviewComments(ctx, owner, name, n, review.ID)
		})
		if err != nil {
			return nil, err
		}
		results = append(results, convertAll(comments, reviewCommentFrom)...)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results, nil
}

// Resolve marks a review conversation resolved or unresolved.
func (s Reviews) Resolve(ctx context.Context, id int64, resolved bool) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		if resolved {
			return c.PullRequests.ResolvePullReviewComment(ctx, owner, name, id)
		}
		return c.PullRequests.UnresolvePullReviewComment(ctx, owner, name, id)
	})
	return repositoryItemError(ctx, s.Client, s.Repository, err, "review comment", strconv.FormatInt(id, 10))
}

// Reply adds a reply to a review comment.
func (s Reviews) Reply(ctx context.Context, n, id int64, body string) (ReviewComment, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, reviewCommentFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullReviewComment, *gitea.Response, error) {
		return c.PullRequests.CreatePullReviewCommentReply(ctx, owner, name, n, id, gitea.CreatePullReviewCommentReplyOptions{Body: body})
	})
	if !IsNotFound(err) {
		return item, Classify(err, "review comment", strconv.FormatInt(id, 10), s.Repository)
	}
	// Gitea answers a bare "not found" for a missing pull request and for a comment on another
	// one, so look the pull request, and with it the repository, up before blaming the comment.
	if _, pullErr := (Pulls{Client: s.Client, Repository: s.Repository}).get(ctx, n); IsNotFound(pullErr) {
		return item, pullErr
	}
	return item, &NotFoundError{Kind: "review comment", Key: strconv.FormatInt(id, 10), Scope: "pull request #" + strconv.FormatInt(n, 10) + " of " + s.Repository, Err: err}
}
