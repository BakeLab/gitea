// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Pull is the stable command representation of a pull request.
type Pull struct {
	Author    string          `json:"author"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Assignees []string        `json:"assignees"`
	Milestone *IssueMilestone `json:"milestone"`
	Labels    []Label         `json:"labels"`
	Deadline  *string         `json:"due_date"`
	ID        int64           `json:"id"`
	Number    int64           `json:"number"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	URL       string          `json:"url"`
	State     string          `json:"state"`
	Merged    bool            `json:"merged"`
	MergedAt  *string         `json:"merged_at"`
	// MergedBy is only in responses for one pull request; lists leave it out.
	MergedBy string `json:"merged_by"`
	// MergeCommitSHA is the commit the merge created on the base branch, or null.
	MergeCommitSHA *string `json:"merge_commit_sha"`
	Mergeable      bool    `json:"mergeable"`
	Draft          bool    `json:"draft"`
	// ReviewDecision summarizes the reviews like gh: approved, changes_requested, review_required
	// or "". Only commands that read the reviews fill it.
	ReviewDecision    string `json:"review_decision"`
	Base              string `json:"base"`
	Head              string `json:"head"`
	HeadSHA           string `json:"head_sha"`
	HeadRepo          string `json:"head_repo"`
	HeadRemoteDeleted bool   `json:"head_remote_deleted"`
	HeadCanPush       bool   `json:"-"`
	HeadCloneURL      string `json:"-"`
	HeadSSHURL        string `json:"-"`
	// AllowMaintainerEdit tells whether maintainers can push to the head branch.
	AllowMaintainerEdit bool `json:"-"`
	// Reviewers and ReviewerTeams are the outstanding review requests. Gitea's own list also
	// names users who already reviewed, so commands that read the reviews replace them with
	// WithReviews.
	Reviewers     []string `json:"reviewers"`
	ReviewerTeams []string `json:"reviewer_teams"`
	// Reviews are the submitted reviews, oldest first, filled by WithReviews and otherwise empty.
	Reviews  []Review `json:"reviews"`
	Comments int64    `json:"comments"`
}

// pullHeadBranch returns the head branch of a pull request and whether Gitea deleted it, in which
// case Gitea answers the pull ref and keeps the branch in the label. An AGit pull request also
// answers the pull ref, but with an empty label: it has no head branch to delete.
func pullHeadBranch(number int64, ref, label string) (branch string, deleted bool) {
	if ref == fmt.Sprintf("refs/pull/%d/head", number) {
		if label == "" {
			return "", false
		}
		branch = label
		if _, name, ok := strings.Cut(label, ":"); ok && name != "" {
			branch = name
		}
		return branch, true
	}
	return ref, false
}

func pullFrom(pull *gitea.PullRequest) Pull {
	var head, base gitea.PRBranchInfo
	if pull.Head != nil {
		head = *pull.Head
	}
	if pull.Base != nil {
		base = *pull.Base
	}
	headBranch, remoteDeleted := pullHeadBranch(pull.Index, head.Ref, head.Name)
	result := Pull{
		Author: login(pull.Poster), CreatedAt: timestamp(deref(pull.Created)), UpdatedAt: timestamp(deref(pull.Updated)),
		Assignees: convertAll(pull.Assignees, login), Milestone: issueMilestoneFrom(pull.Milestone), Labels: convertAll(pull.Labels, labelFrom),
		Deadline: optionalTimestamp(pull.Deadline), ID: pull.ID, Number: pull.Index, Title: pull.Title, Body: pull.Body,
		URL: pull.HTMLURL, State: string(pull.State), Merged: pull.HasMerged, MergedAt: optionalTimestamp(pull.Merged), MergedBy: login(pull.MergedBy),
		MergeCommitSHA: pull.MergedCommitID, Mergeable: pull.Mergeable, Draft: pull.Draft, AllowMaintainerEdit: pull.AllowMaintainerEdit,
		Base: base.Ref, Head: headBranch, HeadSHA: head.Sha, HeadRemoteDeleted: remoteDeleted,
		Reviewers: convertAll(pull.RequestedReviewers, login), Reviews: []Review{}, Comments: int64(pull.Comments),
		ReviewerTeams: convertAll(pull.RequestedReviewersTeams, func(team *gitea.Team) string { return team.Name }),
	}
	// Without a head repository, an AGit pull request, which has no label, has its head in the base
	// repository, while any other lost the fork its head was in.
	repo := head.Repository
	if repo == nil && head.Name == "" {
		repo = base.Repository
	}
	if repo != nil {
		result.HeadRepo, result.HeadCloneURL, result.HeadSSHURL = repo.FullName, repo.CloneURL, repo.SSHURL
		if repo.Permissions != nil {
			result.HeadCanPush = repo.Permissions.Push
		}
	}
	// A deleted head branch is fetched from the base repository's refs/pull, while the head
	// repository still names the fork it came from.
	if remoteDeleted && base.Repository != nil {
		result.HeadCloneURL, result.HeadSSHURL = base.Repository.CloneURL, base.Repository.SSHURL
	}
	return result
}

// PullOptions supplies validated branch and content input for creation.
type PullOptions struct {
	Assignees           []string   `json:"assignees,omitempty"`
	Labels              []int64    `json:"labels,omitempty"`
	Milestone           int64      `json:"milestone,omitempty"`
	Deadline            *time.Time `json:"due_date,omitempty"`
	Title               string     `json:"title"`
	Body                string     `json:"body"`
	Base                string     `json:"base"`
	Head                string     `json:"head"`
	AllowMaintainerEdit *bool      `json:"allow_maintainer_edit,omitempty"`
}

// PullEdit distinguishes omitted fields from explicit empty values.
type PullEdit struct {
	Labels              *[]int64              `json:"-"`
	Title               *string               `json:"title,omitempty"`
	Body                *string               `json:"body,omitempty"`
	State               *string               `json:"state,omitempty"`
	Base                *string               `json:"base,omitempty"`
	AllowMaintainerEdit *bool                 `json:"allow_maintainer_edit,omitempty"`
	Assignees           *[]string             `json:"assignees,omitempty"`
	Milestone           *int64                `json:"-"`
	ReviewerChanges     *ReviewRequestChanges `json:"-"`
	Deadline            *time.Time            `json:"due_date,omitempty"`
	RemoveDeadline      *bool                 `json:"unset_due_date,omitempty"`
	Changes             *MetadataChanges      `json:"-"`
}

// Pulls implements repository pull request operations.
type Pulls struct {
	Client     *transport.Client
	Repository string
}

// PullListOptions filters pull requests. State is open, closed, merged or all.
type PullListOptions struct {
	State, Sort, Author, Base string
	Limit, Page               int
	Milestone                 int64
	Labels                    []int64
	// Draft, Head (BRANCH or OWNER:BRANCH), Assignee, NoAssignee, Search (text in the title or
	// body) and the merged state have no server filter; List applies them to every pull request
	// it reads.
	Draft, NoAssignee      bool
	Head, Assignee, Search string
}

// filtered reports whether a filter needs every pull request read.
func (o PullListOptions) filtered() bool {
	return o.State == "merged" || o.Draft || o.NoAssignee || o.Head != "" || o.Assignee != "" || o.Search != ""
}

// matches applies the filters without a server equivalent to item.
func (o PullListOptions) matches(item Pull) bool {
	owner, branch, qualified := strings.Cut(o.Head, ":")
	if !qualified {
		owner, branch = "", o.Head
	}
	headOwner, _, _ := strings.Cut(item.HeadRepo, "/")
	search := strings.ToLower(o.Search)
	return (o.State != "merged" || item.Merged) && (!o.Draft || item.Draft) &&
		(branch == "" || (item.Head == branch && (owner == "" || strings.EqualFold(owner, headOwner)))) &&
		(o.Assignee == "" || slices.ContainsFunc(item.Assignees, func(name string) bool { return strings.EqualFold(name, o.Assignee) })) &&
		(!o.NoAssignee || len(item.Assignees) == 0) &&
		(search == "" || strings.Contains(strings.ToLower(item.Title), search) || strings.Contains(strings.ToLower(item.Body), search))
}

// List returns a page of pull requests, even when the server caps its page size below the
// limit, and reports whether more pull requests follow.
func (s Pulls) List(ctx context.Context, options PullListOptions) ([]Pull, bool, error) {
	page, err := s.ListPage(ctx, options)
	return page.Items, page.More, err
}

// ListPage is List that also reports how many pull requests match, when the server counts them
// or every pull request was read for a filter without a server equivalent. Such a filter reads
// pages only until one pull request past the requested page matches.
func (s Pulls) ListPage(ctx context.Context, options PullListOptions) (Page[Pull], error) {
	if !options.filtered() {
		return s.listPage(ctx, options)
	}
	query := options
	if query.State == "merged" {
		query.State = "closed"
	}
	return filteredPage(options.Limit, options.Page, func(limit, page int) (Page[Pull], error) {
		query.Limit, query.Page = limit, page
		return s.listPage(ctx, query)
	}, options.matches)
}

func (s Pulls) listPage(ctx context.Context, options PullListOptions) (Page[Pull], error) {
	query := url.Values{"state": {options.State}}
	if options.Sort != "" {
		query.Set("sort", options.Sort)
	}
	if options.Author != "" {
		query.Set("poster", options.Author)
	}
	if options.Base != "" {
		query.Set("base_branch", options.Base)
	}
	if options.Milestone > 0 {
		query.Set("milestone", strconv.FormatInt(options.Milestone, 10))
	}
	for _, label := range options.Labels {
		query.Add("labels", strconv.FormatInt(label, 10))
	}

	// TODO(sdk): ListPullRequestsOptions lacks the poster, base_branch, and labels filters.
	page, err := rawPage[*gitea.PullRequest](ctx, s.Client, "repos/"+s.Repository+"/pulls", query, options.Limit, options.Page)
	if err != nil {
		if IsNotFound(err) {
			repo, lookupErr := (Repositories{Client: s.Client}).get(ctx, s.Repository)
			if lookupErr == nil && repo.Empty && repo.HasPullRequests {
				return Page[Pull]{Items: []Pull{}, Counted: true}, nil // Gitea answers 404 for the pull requests of a repository without commits
			}
			if unitErr := unitOffIn(repo, lookupErr, s.Repository, err, UnitPulls); unitErr != nil {
				return Page[Pull]{}, unitErr
			}
		}
		return Page[Pull]{}, Classify(err, "repository", s.Repository, "")
	}
	return convertPage(page, pullFrom), nil
}

// View retrieves one pull request.
func (s Pulls) View(ctx context.Context, n int64) (Pull, error) {
	pull, err := s.get(ctx, n)
	if err != nil {
		return Pull{}, err
	}
	return pullFrom(pull), nil
}

// WithReviews returns p with its reviewers, reviewer teams, reviews and review decision taken
// from reviews, all of its reviews. While the protection of the base branch requires approvals,
// the decision is approved only with as many official ones as Gitea counts for merging, and
// review_required with fewer.
func (s Pulls) WithReviews(ctx context.Context, p Pull, reviews []Review) Pull {
	return s.withReviews(ctx, p, reviews, &approvalRules{})
}

func (s Pulls) withReviews(ctx context.Context, p Pull, reviews []Review, rules *approvalRules) Pull {
	p.Reviewers, p.ReviewerTeams = RequestedReviewers(reviews)
	p.Reviews = slices.DeleteFunc(slices.Clone(reviews), func(review Review) bool {
		return review.Author == "" || review.State == string(gitea.ReviewStatePending) || review.State == string(gitea.ReviewStateRequestReview)
	})
	p.ReviewDecision = ReviewDecision(reviews, len(p.ReviewerTeams) > 0)
	if p.ReviewDecision == "changes_requested" || p.ReviewDecision == "review_required" || p.Base == "" {
		return p
	}
	required, rule := rules.branch(ctx, s, p.Base)
	approvals := OfficialApprovals(reviews, false)
	if approvals >= required && OfficialApprovals(reviews, true) < required && rules.ignoresStale(ctx, s, rule) {
		approvals = OfficialApprovals(reviews, true)
	}
	if approvals < required {
		p.ReviewDecision = "review_required"
	}
	return p
}

// approvalRules remembers, for the pull requests of one command, what the protection of each base
// branch asks of approvals, so that each base and rule is read once.
type approvalRules struct {
	branches onceByKey[branchApprovals]
	rules    onceByKey[bool]
}

// onceByKey runs read once per key, also for concurrent callers, and remembers its value.
type onceByKey[T any] struct {
	mu    sync.Mutex
	reads map[string]func() T
}

func (o *onceByKey[T]) get(key string, read func() T) T {
	o.mu.Lock()
	value, ok := o.reads[key]
	if !ok {
		if o.reads == nil {
			o.reads = map[string]func() T{}
		}
		value = sync.OnceValue(read)
		o.reads[key] = value
	}
	o.mu.Unlock()
	return value()
}

type branchApprovals struct {
	required int64
	rule     string
}

// branch returns how many approvals the protection of base requires and the name of its rule. A
// branch that cannot be read requires none.
func (r *approvalRules) branch(ctx context.Context, s Pulls, base string) (int64, string) {
	found := r.branches.get(base, func() branchApprovals {
		var branch *gitea.Branch
		_ = repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (resp *gitea.Response, err error) {
			branch, resp, err = c.Repositories.GetRepoBranch(ctx, owner, name, base)
			return resp, err
		})
		if branch == nil {
			return branchApprovals{}
		}
		return branchApprovals{required: branch.RequiredApprovals, rule: branch.EffectiveBranchProtectionName}
	})
	return found.required, found.rule
}

// ignoresStale tells whether the protection rule leaves out approvals of an older head commit.
// Gitea shows rules only to repository admins and leaves out the rule name for others; then the
// approvals count as with Gitea's default, which keeps them.
func (r *approvalRules) ignoresStale(ctx context.Context, s Pulls, rule string) bool {
	if rule == "" {
		return false
	}
	return r.rules.get(rule, func() bool {
		// TODO(sdk): BranchProtection lacks ignore_stale_approvals.
		var protection struct {
			IgnoreStaleApprovals bool `json:"ignore_stale_approvals"`
		}
		err := getJSON(ctx, s.Client, "repos/"+s.Repository+"/branch_protections/"+url.PathEscape(rule), &protection)
		return err == nil && protection.IgnoreStaleApprovals
	})
}

// Detail reads, a few pull requests at a time, what a listing leaves out: with view, each pull
// request again, for merged_by; with reviews, its reviews, for WithReviews.
func (s Pulls) Detail(ctx context.Context, items []Pull, view, reviews bool) ([]Pull, error) {
	detailed := slices.Clone(items)
	service := Reviews{Client: s.Client, Repository: s.Repository}
	rules := &approvalRules{}
	err := concurrently(len(items), func(i int) (err error) {
		if view {
			if detailed[i], err = s.View(ctx, items[i].Number); err != nil {
				return err
			}
		}
		if reviews {
			all, err := service.All(ctx, items[i].Number)
			if err != nil {
				return err
			}
			detailed[i] = s.withReviews(ctx, detailed[i], all, rules)
		}
		return nil
	})
	return detailed, err
}

func (s Pulls) get(ctx context.Context, n int64) (*gitea.PullRequest, error) {
	item, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.GetPullRequest(ctx, owner, name, n)
	})
	return item, pullReadError(ctx, s.Client, s.Repository, err, n)
}

// pullError names pull request n of repository in the failure of a request about it, or the
// repository or its turned-off pull requests when that is what Gitea's bare 404 means. A 404 that
// names what is missing, such as the pull request or a branch, is no such answer.
func pullError(ctx context.Context, client *transport.Client, repository string, err error, n int64) error {
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && (apiErr.Message == "" || apiErr.Message == "not found") {
		if unitErr := unitError(ctx, client, repository, err, UnitPulls); unitErr != nil {
			return unitErr
		}
	}
	return Classify(err, "pull request", "#"+strconv.FormatInt(n, 10), repository)
}

// pullReadError is pullError that tells the number of an issue apart from one that does not
// exist, for requests that Gitea refuses before changing anything when pull request n is missing.
func pullReadError(ctx context.Context, client *transport.Client, repository string, err error, n int64) error {
	err = pullError(ctx, client, repository, err, n)
	if notFound, ok := errors.AsType[*NotFoundError](err); ok && notFound.Kind == "pull request" {
		if kind, _ := (Issues{Client: client, Repository: repository}).Kind(ctx, n); kind == KindIssue {
			return &NotPullError{Number: n, Repository: repository, Err: err}
		}
	}
	return err
}

// issues serves the pull requests through the issue endpoints.
func (s Pulls) issues() Issues {
	return Issues{Client: s.Client, Repository: s.Repository, Pulls: true}
}

// ListOpen returns every open pull request in the repository.
func (s Pulls) ListOpen(ctx context.Context) ([]Pull, error) {
	return listEvery(50, func(limit, page int) (Page[Pull], error) {
		return s.ListPage(ctx, PullListOptions{State: "open", Limit: limit, Page: page})
	})
}

// NewestClosed returns the most recently updated closed or merged pull request that keep
// accepts, and whether there is one. It reads pages newest first and stops at the first match.
func (s Pulls) NewestClosed(ctx context.Context, keep func(Pull) bool) (Pull, bool, error) {
	for page := 1; page <= maxListPages; page++ {
		items, _, err := s.List(ctx, PullListOptions{State: "closed", Sort: "recentupdate", Limit: 50, Page: page})
		if err != nil {
			return Pull{}, false, err
		}
		if len(items) == 0 {
			return Pull{}, false, nil
		}
		for _, item := range items {
			if keep(item) {
				return item, true, nil
			}
		}
	}
	return Pull{}, false, nil
}

// Create opens a pull request.
func (s Pulls) Create(ctx context.Context, opts PullOptions) (Pull, error) {
	// TODO(sdk): CreatePullRequestOption lacks allow_maintainer_edit.
	var pull gitea.PullRequest
	if err := sendJSON(ctx, s.Client, http.MethodPost, "repos/"+s.Repository+"/pulls", opts, &pull); err != nil {
		return Pull{}, s.existingPullError(ctx, s.createError(ctx, err, opts.Head), opts)
	}
	return pullFrom(&pull), nil
}

// existingPullError links the open pull request that already exists for the branches of opts,
// which Gitea refuses to duplicate without saying which it is.
func (s Pulls) existingPullError(ctx context.Context, err error, opts PullOptions) error {
	conflict, ok := errors.AsType[*ConflictError](err)
	if !ok || conflict.Reason != ReasonExists || conflict.Kind != "pull request" {
		return err
	}
	existing, lookupErr := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.GetPullRequestByBaseHead(ctx, owner, name, opts.Base, opts.Head)
	})
	if lookupErr == nil && existing.HTMLURL != "" {
		conflict.Hint = "see " + existing.HTMLURL
	}
	return err
}

// PullRepository is what creating a pull request needs to know about the repository it goes into.
type PullRepository struct {
	Repository    string
	DefaultBranch string
	PullsEnabled  bool
	// CanPush is whether the account may push branches to it. A response without permissions, as
	// for an anonymous request, leaves it to the push.
	CanPush bool
}

// Base reads the repository of the pull requests, for a new one.
func (s Pulls) Base(ctx context.Context) (PullRepository, error) {
	item, err := (Repositories{Client: s.Client}).get(ctx, s.Repository)
	if err != nil {
		return PullRepository{}, err
	}
	return pullRepositoryFrom(s.Repository, item), nil
}

func pullRepositoryFrom(repository string, item *gitea.Repository) PullRepository {
	return PullRepository{Repository: repository, DefaultBranch: item.DefaultBranch, PullsEnabled: item.HasPullRequests, CanPush: item.Permissions == nil || item.Permissions.Push}
}

// CheckEnabled returns the UnitError of a repository with its pull requests turned off, so that a
// command can refuse before it prompts or pushes a branch.
func (r PullRepository) CheckEnabled() error {
	if !r.PullsEnabled {
		return &UnitError{Repository: r.Repository, Unit: UnitPulls}
	}
	return nil
}

// PushableFork reports whether repo is a fork of the pull requests' repository that the account
// may push to, so that it can hold the head branch of a new pull request.
func (s Pulls) PushableFork(ctx context.Context, repo string) (bool, error) {
	item, err := (Repositories{Client: s.Client}).get(ctx, repo)
	if err != nil {
		return false, err
	}
	return item.Parent != nil && strings.EqualFold(item.Parent.FullName, s.Repository) && permissionsFrom(item).Push, nil
}

// CheckUsers verifies that the named assignees and reviewers, such as those of a pull request
// about to be created, exist, so that an unknown name fails before a branch is pushed or the pull
// request exists. A user the account may not see is left for Gitea to refuse, and so is a reviewer
// named by email address, which Gitea looks up by email; assignees are taken by name only.
func (s Pulls) CheckUsers(ctx context.Context, assignees, reviewers []string) error {
	names := slices.Concat(assignees, slices.DeleteFunc(slices.Clone(reviewers), func(name string) bool { return strings.Contains(name, "@") }))
	checked := map[string]bool{}
	for _, name := range names {
		if checked[strings.ToLower(name)] {
			continue
		}
		checked[strings.ToLower(name)] = true
		_, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
			return c.Users.GetUserInfo(ctx, name)
		})
		if IsNotFound(err) {
			return &NotFoundError{Kind: "user", Key: strconv.Quote(name), Err: err}
		}
		if err != nil && !IsForbidden(err) {
			return err
		}
	}
	return nil
}

// createError explains a refused pull request creation. Gitea looks a head of another owner,
// OWNER:BRANCH, up in that owner's fork, and answers a bare "not found" for an unknown owner and
// a missing branch without saying it looked in the fork. It answers a missing repository with a
// bare "not found" too.
func (s Pulls) createError(ctx context.Context, err error, head string) error {
	if unitErr := unitError(ctx, s.Client, s.Repository, err, UnitPulls); unitErr != nil {
		return unitErr
	}
	owner, branch, qualified := strings.Cut(head, ":")
	baseOwner, _, _ := strings.Cut(s.Repository, "/")
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !qualified || strings.EqualFold(owner, baseOwner) || !ok {
		return Classify(err, "pull request", "", s.Repository)
	}
	// Gitea refuses the head of an owner without a fork with a bare 400.
	if strings.Contains(apiErr.Message, "does not have a fork of the base repository") {
		return &NotFoundError{Kind: "fork", Key: "of " + s.Repository + " owned by " + owner, Err: err}
	}
	if apiErr.Status != http.StatusNotFound {
		return Classify(err, "pull request", "", s.Repository)
	}
	if apiErr.Message == "not found" {
		return &NotFoundError{Kind: "user or organization", Key: strconv.Quote(owner), Err: err}
	}
	classified := Classify(err, "pull request", "", s.Repository)
	if notFound, ok := errors.AsType[*NotFoundError](classified); ok && notFound.Kind == "branch" {
		return &NotFoundError{Kind: "branch", Key: strconv.Quote(branch), Scope: "the fork of " + s.Repository + " owned by " + owner, Err: err}
	}
	return classified
}

// Edit updates only explicitly supplied resource fields.
func (s Pulls) Edit(ctx context.Context, n int64, opts PullEdit) (Pull, error) {
	return s.EditFrom(ctx, n, opts, nil)
}

// EditFrom is Edit for pull request n as before holds it just ahead of the edit, or nil when it
// was not read. When an edit of several fields fails, the pull request read back is compared with
// before to tell what was saved; without before, such an edit reads it first.
func (s Pulls) EditFrom(ctx context.Context, n int64, opts PullEdit, before *Pull) (Pull, error) {
	if opts.ReviewerChanges == nil {
		return s.edit(ctx, n, opts, before, nil)
	}
	reviews, changes := Reviews{Client: s.Client, Repository: s.Repository}, opts.ReviewerChanges
	opts.ReviewerChanges = nil
	if opts == (PullEdit{}) {
		return reviews.ChangeRequests(ctx, n, *changes)
	}
	// Gitea takes the request of a reviewer it already asked, and the withdrawal of one it did not,
	// without a change: only the requests as they were tell a failed edit whether they were saved.
	if err := changes.Validate(); err != nil {
		return Pull{}, err
	}
	held, err := reviews.All(ctx, n)
	if err != nil {
		return Pull{}, err
	}
	if err := reviews.changeRequests(ctx, n, *changes); err != nil {
		return Pull{}, err
	}
	var saved []string
	if changesRequests(held, *changes) {
		saved = []string{"the review requests"}
	}
	item, err := s.edit(ctx, n, opts, before, saved)
	if _, partial := errors.AsType[*PartialEditError](err); err != nil && !partial && len(saved) > 0 {
		return Pull{}, fmt.Errorf("review requests were updated, but editing other pull request fields failed: %w", err)
	}
	return item, err
}

// changesRequests reports whether changes change the review requests among reviews. A reviewer
// named by email address is taken to change them, since the reviews name users by login.
func changesRequests(reviews []Review, changes ReviewRequestChanges) bool {
	users, teams := RequestedReviewers(reviews)
	for _, names := range []struct{ held, add, remove []string }{
		{users, changes.Add.Users, changes.Remove.Users}, {teams, changes.Add.Teams, changes.Remove.Teams},
	} {
		held := func(name string) bool {
			return !strings.Contains(name, "@") && slices.ContainsFunc(names.held, func(h string) bool { return strings.EqualFold(h, name) })
		}
		if slices.ContainsFunc(names.add, func(name string) bool { return !held(name) }) ||
			slices.ContainsFunc(names.remove, func(name string) bool { return held(name) || strings.Contains(name, "@") }) {
			return true
		}
	}
	return false
}

// edit sends opts after the changes that saved names were saved.
func (s Pulls) edit(ctx context.Context, n int64, opts PullEdit, before *Pull, saved []string) (Pull, error) {
	if before == nil && editFieldCount(opts) > 1 {
		// Gitea saves the fields of a request one after another and keeps those it saved before
		// a failure: only the pull request as it was tells them from those it already held.
		item, err := s.View(ctx, n)
		if err != nil {
			return Pull{}, err
		}
		before = &item
	}
	if opts.Changes != nil {
		issues, edit := s.issues(), IssueEdit{Labels: opts.Labels, Assignees: opts.Assignees, Changes: opts.Changes}
		var err error
		if before != nil {
			edit, err = issues.applyChangesTo(ctx, before.Labels, before.Assignees, edit)
		} else {
			edit, err = issues.applyChanges(ctx, n, edit)
		}
		if err != nil {
			return Pull{}, err
		}
		opts.Labels, opts.Assignees, opts.Changes = edit.Labels, edit.Assignees, nil
	}
	run := &editRun{Pulls: s, n: n, opts: opts, before: before, saved: saved}
	if opts.Milestone != nil || opts.Labels != nil {
		return run.throughIssue(ctx)
	}
	item, err := s.editPull(ctx, n, opts)
	if err != nil {
		run.failures = append(run.failures, editFailure{sent: opts, err: err})
		return run.failed(ctx)
	}
	return item, nil
}

// editFieldCount counts the fields that opts changes, the review requests aside.
func editFieldCount(opts PullEdit) int {
	count := len(editFieldNames(opts))
	if changes := opts.Changes; changes != nil {
		if opts.Labels == nil && len(changes.AddLabels)+len(changes.RemoveLabels) > 0 {
			count++
		}
		if opts.Assignees == nil && len(changes.AddAssignees)+len(changes.RemoveAssignees) > 0 {
			count++
		}
	}
	return count
}

// editPull sends opts to the pull request endpoint.
func (s Pulls) editPull(ctx context.Context, n int64, opts PullEdit) (Pull, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, pullFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.EditPullRequest(ctx, owner, name, n, editPullOption(opts))
	})
	return item, pullError(ctx, s.Client, s.Repository, err, n)
}

// editRun follows the requests that edit pull request n with opts.
type editRun struct {
	Pulls
	n    int64
	opts PullEdit
	// before is the pull request ahead of the edit, or nil for an edit of one field.
	before *Pull
	// saved names the changes that requests ahead of the edit saved, such as the review requests.
	saved []string
	// answered holds the requests that succeeded, with the pull request as each answered.
	answered []answeredEdit
	failures []editFailure
	// skipped is set when a failure kept the later requests from being sent.
	skipped bool
	// labelsSent is set when the labels were sent, labels are those that Gitea took, and
	// labelsErr is the error of a refused label change.
	labelsSent bool
	labels     []Label
	labelsErr  error
	// after is the pull request read back after a failure, or nil when readErr kept it from being
	// read; afterLabels is set when it was read after the labels were sent.
	after       *Pull
	afterLabels bool
	readErr     error
	read        bool
}

// answeredEdit is a request that sent the changes of sent, answered with item.
type answeredEdit struct {
	sent PullEdit
	item Pull
}

// editFailure is a request that sent the changes of sent and failed with err.
type editFailure struct {
	sent PullEdit
	err  error
}

// throughIssue edits the pull request when its milestone or labels change. The PR endpoint
// ignores zero milestones and cannot clear the labels; its issue endpoint can, and lets the common
// fields share the same mutation. The labels go last: Gitea refuses label changes from people
// who cannot write to the pull requests, and still takes the other changes from the author.
func (r *editRun) throughIssue(ctx context.Context) (Pull, error) {
	issues := r.issues()
	opts := r.opts
	common := PullEdit{
		Title: opts.Title, Body: opts.Body, Assignees: opts.Assignees, Milestone: opts.Milestone,
		Deadline: opts.Deadline, RemoveDeadline: opts.RemoveDeadline,
	}
	pull := PullEdit{State: opts.State, Base: opts.Base, AllowMaintainerEdit: opts.AllowMaintainerEdit}
	if common != (PullEdit{}) {
		issue, err := issues.Edit(ctx, r.n, IssueEdit{
			Title: common.Title, Body: common.Body, Assignees: common.Assignees, Milestone: common.Milestone,
			Deadline: common.Deadline, RemoveDeadline: common.RemoveDeadline,
		})
		if err != nil {
			// Neither the fields of the pull request nor the labels go after a failure here.
			r.failures, r.skipped = append(r.failures, editFailure{sent: common, err: err}), true
			return r.failed(ctx)
		}
		r.answered = append(r.answered, answeredEdit{sent: common, item: pullFromIssue(issue)})
	}
	var result Pull
	if pull != (PullEdit{}) {
		item, err := r.editPull(ctx, r.n, pull)
		if err != nil {
			r.failures = append(r.failures, editFailure{sent: pull, err: err})
		} else {
			result = item
			r.answered = append(r.answered, answeredEdit{sent: pull, item: item})
		}
	}
	if opts.Labels != nil {
		// After a failure, the labels go only with other changes that stand, so that a refused
		// edit leaves the pull request as it was.
		if len(r.failures) == 0 || len(r.answered) > 0 || len(r.saved) > 0 || r.savedSome(ctx) {
			r.labelsSent = true
			if r.labels, r.labelsErr = issues.replaceLabels(ctx, r.n, *opts.Labels); r.labelsErr == nil {
				result.Labels = r.labels
			}
		} else {
			r.skipped = true
		}
	}
	if len(r.failures) > 0 || r.labelsErr != nil {
		return r.failed(ctx)
	}
	if result.Number == 0 {
		// The issue endpoint answers with an issue: the pull request goes with the edit.
		item, err := r.View(ctx, r.n)
		if err != nil {
			return Pull{}, fmt.Errorf("pull request #%d was edited, but reading it back failed: %w", r.n, err)
		}
		return item, nil
	}
	return result, nil
}

// readBack reads the pull request after a failure, once.
func (r *editRun) readBack(ctx context.Context) {
	if r.read {
		return
	}
	r.read, r.afterLabels = true, r.labelsSent
	item, err := r.View(ctx, r.n)
	if err != nil {
		r.readErr = err
		return
	}
	r.after = &item
}

// savedSome reports whether the pull request, read back after a failure, holds a change that the
// edit saved.
func (r *editRun) savedSome(ctx context.Context) bool {
	if r.before == nil {
		return false
	}
	r.readBack(ctx)
	if r.after == nil {
		return false
	}
	saved, _ := editOutcome(r.opts, *r.before, *r.after)
	return len(saved) > 0
}

// failed reports an edit that a request failed. It names the changes that were saved and, read
// back, those the pull request does not hold. The error of a request that sent one change names
// that change; that of one which sent several names none, since Gitea does not say which it
// refused. A change the pull request held before the edit is named in neither. Only an edit
// that went to its end and saved something returns the pull request.
func (r *editRun) failed(ctx context.Context) (Pull, error) {
	issues := r.issues()
	if r.before == nil {
		// An edit of one field: with nothing to compare with, the error goes as Gitea gave it.
		if r.labelsErr != nil {
			if len(r.saved) == 0 {
				return Pull{}, issues.labelsError(ctx, r.n, KindPull, "", r.labelsErr)
			}
			return Pull{}, &PartialEditError{Saved: r.saved, Errs: []error{r.labelsFailure(ctx, false)}, Number: r.n}
		}
		if len(r.saved) == 0 {
			return Pull{}, r.failures[0].err
		}
		return Pull{}, &PartialEditError{Saved: r.saved, Errs: []error{fmt.Errorf("the edit failed: %w", r.failures[0].err)}, Number: r.n}
	}
	r.readBack(ctx)
	saved := slices.Clone(r.saved)
	var failed []string
	fields := r.opts
	fields.Labels = nil
	if r.after != nil {
		stored, missed := editOutcome(fields, *r.before, *r.after)
		saved, failed = append(saved, stored...), missed
	} else {
		// Unread, the pull request holds at least what the requests that succeeded answered, and
		// lacks what no request sent.
		sent := map[string]bool{}
		for _, answered := range r.answered {
			stored, _ := editOutcome(answered.sent, *r.before, answered.item)
			saved = append(saved, stored...)
			for _, name := range editFieldNames(answered.sent) {
				sent[name] = true
			}
		}
		for _, failure := range r.failures {
			for _, name := range editFieldNames(failure.sent) {
				sent[name] = true
			}
		}
		_, missed := editOutcome(fields, *r.before, *r.before)
		failed = slices.DeleteFunc(missed, func(name string) bool { return sent[name] })
	}
	// labelsKnown is set when the labels are known to be stored or not after their request.
	labelsKnown := r.labelsErr == nil || (r.after != nil && r.afterLabels)
	labelsHeld := r.opts.Labels == nil || sameLabels(r.before.Labels, *r.opts.Labels)
	labelsStored := false
	if !labelsHeld {
		switch {
		case !r.labelsSent:
			failed = append(failed, "the labels")
		case r.labelsErr == nil:
			saved = append(saved, "the labels")
		case labelsKnown && sameLabels(r.after.Labels, *r.opts.Labels):
			// Gitea stored the labels, and then failed: a server error or a lost answer.
			labelsStored = true
			saved = append(saved, "the labels")
		}
	}
	var errs, plain []error
	for _, failure := range r.failures {
		plain = append(plain, failure.err)
		names := editFieldNames(failure.sent)
		if r.after != nil && len(names) == 1 && slices.Contains(failed, names[0]) {
			failed = slices.DeleteFunc(failed, func(name string) bool { return name == names[0] })
			errs = append(errs, fmt.Errorf("changing %s failed: %w", names[0], failure.err))
			continue
		}
		errs = append(errs, fmt.Errorf("the edit failed: %w", failure.err))
	}
	if r.labelsErr != nil {
		plain = append(plain, issues.labelsError(ctx, r.n, KindPull, "", r.labelsErr))
		if labelsHeld || labelsStored {
			errs = append(errs, fmt.Errorf("the edit failed: %w", r.labelsErr))
		} else {
			errs = append(errs, r.labelsFailure(ctx, !labelsKnown))
		}
	}
	if len(saved) == 0 && r.after != nil {
		// Nothing was saved: the errors go as Gitea gave them.
		return Pull{}, errors.Join(plain...)
	}
	partial := &PartialEditError{Saved: saved, Failed: failed, Errs: errs, Number: r.n, ReadErr: r.readErr}
	if r.after == nil || r.skipped {
		return Pull{}, partial
	}
	item := *r.after
	if r.labelsSent && r.labelsErr == nil {
		item.Labels = r.labels
	}
	return item, partial
}

// labelsFailure words the failed label change of the edit; unknown is set when no read of the
// pull request after the label request tells whether Gitea stored them before it failed.
func (r *editRun) labelsFailure(ctx context.Context, unknown bool) error {
	issues := r.issues()
	if refusal := issues.labelsRefusal(ctx, r.n, KindPull, r.labelsErr); refusal != "" {
		return &RefusedError{Text: "the labels were left unchanged: " + refusal, Err: r.labelsErr}
	}
	if !unknown {
		return fmt.Errorf("changing the labels failed: %w", r.labelsErr)
	}
	if r.readErr != nil {
		// The error of the read points to the current state.
		return fmt.Errorf("the labels may or may not have been saved: %w", r.labelsErr)
	}
	return fmt.Errorf("the labels may or may not have been saved: %w; gt pr view %d shows the current state", r.labelsErr, r.n)
}

// pullFromIssue is the pull request that the issue endpoint answered with.
func pullFromIssue(issue Issue) Pull {
	return Pull{
		Number: issue.Number, Title: issue.Title, Body: issue.Body, State: issue.State, Assignees: issue.Assignees,
		Milestone: issue.Milestone, Deadline: issue.Deadline, Labels: issue.Labels,
	}
}

// PartialEditError reports an edit that Gitea took only in part: the changes that Saved names
// were saved, and those that Failed names were not. Errs are the errors of the requests that
// failed; one that names its change, as that of a request which sent only it, keeps the change
// out of Failed. ReadErr is set when pull request Number could not be read back after the
// failure: the changes besides Saved and Failed may or may not have been saved then.
type PartialEditError struct {
	Saved, Failed []string
	Errs          []error
	Number        int64
	ReadErr       error
}

func (e *PartialEditError) Error() string {
	var parts []string
	if len(e.Failed) > 0 {
		parts = append(parts, changesClause(e.Failed, "not changed"))
	}
	for _, err := range e.Errs {
		parts = append(parts, err.Error())
	}
	if e.ReadErr != nil {
		changes := "the changes"
		if len(e.Saved)+len(e.Failed) > 0 {
			changes = "the other changes"
		}
		parts = append(parts, fmt.Sprintf("%s may or may not have been saved, as reading the pull request back failed: %v; gt pr view %d shows the current state", changes, e.ReadErr, e.Number))
	}
	text := strings.Join(parts, "; ")
	if len(e.Saved) > 0 {
		text = changesClause(e.Saved, "saved") + ", but " + text
	}
	return text
}

func (e *PartialEditError) Unwrap() []error {
	if e.ReadErr == nil {
		return e.Errs
	}
	return append(slices.Clone(e.Errs), e.ReadErr)
}

// changesClause words that the changes names were done: "the title was saved".
func changesClause(names []string, done string) string {
	verb := "were"
	if len(names) == 1 && !strings.HasSuffix(names[0], "s") {
		verb = "was"
	}
	return joinWords(names) + " " + verb + " " + done
}

// joinWords lists words in a sentence: "a", "a and b", "a, b and c".
func joinWords(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// editField is a change that an edit can ask for, named for messages.
type editField struct {
	name string
	set  bool
	// stored reports whether a pull request holds the change.
	stored func(Pull) bool
}

// editFields lists the changes to the fields of the pull request that opts can ask for, in the
// order of messages. The labels change through their own request.
func editFields(opts PullEdit) []editField {
	return []editField{
		{name: "the title", set: opts.Title != nil, stored: func(item Pull) bool {
			return strings.TrimSpace(item.Title) == strings.TrimSpace(*opts.Title)
		}},
		{name: "the body", set: opts.Body != nil, stored: func(item Pull) bool { return item.Body == *opts.Body }},
		{name: "the assignees", set: opts.Assignees != nil, stored: func(item Pull) bool {
			return sameFold(item.Assignees, *opts.Assignees)
		}},
		{name: "the milestone", set: opts.Milestone != nil, stored: func(item Pull) bool {
			var id int64
			if item.Milestone != nil {
				id = item.Milestone.ID
			}
			return id == *opts.Milestone
		}},
		{name: "the due date", set: opts.Deadline != nil || opts.RemoveDeadline != nil, stored: func(item Pull) bool {
			if opts.Deadline == nil {
				return !*opts.RemoveDeadline || item.Deadline == nil
			}
			if item.Deadline == nil {
				return false
			}
			// gt sends the end of the day in UTC. Gitea keeps the end of that day in the offset it
			// was sent with and answers in its own time zone, so the instants match; a server that
			// kept the end of the day in its own time zone answers with that date in its offset.
			due, err := time.Parse(time.RFC3339, *item.Deadline)
			want := opts.Deadline.UTC()
			return err == nil && (due.Equal(want) || due.Format(time.DateOnly) == want.Format(time.DateOnly))
		}},
		{name: "the state", set: opts.State != nil, stored: func(item Pull) bool { return item.State == *opts.State }},
		{name: "the base branch", set: opts.Base != nil, stored: func(item Pull) bool { return item.Base == *opts.Base }},
		{name: "whether maintainers can edit", set: opts.AllowMaintainerEdit != nil, stored: func(item Pull) bool {
			return item.AllowMaintainerEdit == *opts.AllowMaintainerEdit
		}},
	}
}

// editFieldNames names the changes that opts asks for.
func editFieldNames(opts PullEdit) []string {
	var names []string
	for _, field := range editFields(opts) {
		if field.set {
			names = append(names, field.name)
		}
	}
	if opts.Labels != nil {
		names = append(names, "the labels")
	}
	return names
}

// editOutcome names the changes to the fields of the pull request that opts asks for and after
// holds, and those it lacks, leaving out those that before held already.
func editOutcome(opts PullEdit, before, after Pull) (stored, missed []string) {
	for _, field := range editFields(opts) {
		switch {
		case !field.set || field.stored(before):
		case field.stored(after):
			stored = append(stored, field.name)
		default:
			missed = append(missed, field.name)
		}
	}
	return stored, missed
}

// sameLabels reports whether labels are those of ids, ignoring order.
func sameLabels(labels []Label, ids []int64) bool {
	held := make([]int64, len(labels))
	for i, label := range labels {
		held[i] = label.ID
	}
	slices.Sort(held)
	return slices.Equal(slices.Compact(held), slices.Compact(slices.Sorted(slices.Values(ids))))
}

// sameFold reports whether a and b hold the same names, ignoring case and order.
func sameFold(a, b []string) bool {
	in := func(names []string) func(string) bool {
		return func(name string) bool {
			return !slices.ContainsFunc(names, func(other string) bool { return strings.EqualFold(other, name) })
		}
	}
	return !slices.ContainsFunc(a, in(b)) && !slices.ContainsFunc(b, in(a))
}

func editPullOption(opts PullEdit) gitea.EditPullRequestOption {
	option := gitea.EditPullRequestOption{
		Body: opts.Body, Deadline: opts.Deadline, RemoveDeadline: opts.RemoveDeadline, AllowMaintainerEdit: opts.AllowMaintainerEdit,
	}
	if opts.Title != nil {
		option.Title = *opts.Title
	}
	if opts.Base != nil {
		option.Base = *opts.Base
	}
	if opts.Assignees != nil {
		option.Assignees = *opts.Assignees
	}
	if opts.State != nil {
		state := gitea.StateType(*opts.State)
		option.State = &state
	}
	return option
}

// Diff returns the pull request's patch, which the caller must close.
func (s Pulls) Diff(ctx context.Context, n int64) (io.ReadCloser, error) {
	diff, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]byte, *gitea.Response, error) {
		return c.PullRequests.GetPullRequestDiff(ctx, owner, name, n, gitea.PullRequestDiffOptions{})
	})
	if err != nil {
		return nil, pullReadError(ctx, s.Client, s.Repository, err, n)
	}
	return io.NopCloser(bytes.NewReader(diff)), nil
}

// DeleteHead deletes the head branch of a pull request in its head repository.
func (s Pulls) DeleteHead(ctx context.Context, item Pull) error {
	if item.HeadRemoteDeleted || item.Head == "" {
		return nil
	}
	repo := cmp.Or(item.HeadRepo, s.Repository)
	_, err := repoCall(ctx, s.Client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) (bool, *gitea.Response, error) {
		return c.Repositories.DeleteRepoBranch(ctx, owner, name, item.Head)
	})
	return err
}

// Files lists the paths of the files a pull request changes.
func (s Pulls) Files(ctx context.Context, n int64) ([]string, error) {
	owner, name, err := splitRepository(s.Repository)
	if err != nil {
		return nil, err
	}
	files, err := callAll(ctx, s.Client, 100, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.ChangedFile, *gitea.Response, error) {
		return c.PullRequests.ListPullRequestFiles(ctx, owner, name, n, gitea.ListPullRequestFilesOptions{ListOptions: list})
	})
	if err != nil {
		return nil, pullReadError(ctx, s.Client, s.Repository, err, n)
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if file != nil {
			paths = append(paths, file.Filename)
		}
	}
	return paths, nil
}

// MergeOptions supplies merge strategy and optional commit text. Auto merges once the required
// checks succeed instead of now.
type MergeOptions struct {
	Method, Title, Message string
	DeleteBranch, Auto     bool
	// HeadSHA is the head commit the user chose to merge; Gitea refuses the merge once the head
	// moved past it. Empty merges the current head.
	HeadSHA string
	// Commit is the commit that merged the pull request outside Gitea, for the manually-merged method.
	Commit string
}

// MergeSettings are the merge styles a repository allows, in Gitea's order, and its merge defaults.
type MergeSettings struct {
	Styles       []string
	Default      string
	DeleteBranch bool
}

// MergeSettings reads the merge styles the repository allows and its default style and branch
// deletion. A default style that is not allowed falls back to the first allowed one.
func (s Pulls) MergeSettings(ctx context.Context) (MergeSettings, error) {
	repo, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Repository, *gitea.Response, error) {
		return c.Repositories.GetRepo(ctx, owner, name)
	})
	if err != nil {
		return MergeSettings{}, err
	}
	settings := MergeSettings{Default: string(repo.DefaultMergeStyle), DeleteBranch: repo.DefaultDeleteBranchAfterMerge}
	for _, style := range []struct {
		name    gitea.MergeStyle
		allowed bool
	}{
		{gitea.MergeStyleMerge, repo.AllowMerge},
		{gitea.MergeStyleRebase, repo.AllowRebase},
		{gitea.MergeStyleRebaseMerge, repo.AllowRebaseMerge},
		{gitea.MergeStyleSquash, repo.AllowSquash},
		{gitea.MergeStyleFastForwardOnly, repo.AllowFastForwardOnlyMerge},
	} {
		if style.allowed {
			settings.Styles = append(settings.Styles, string(style.name))
		}
	}
	if !slices.Contains(settings.Styles, settings.Default) && len(settings.Styles) > 0 {
		settings.Default = settings.Styles[0]
	}
	return settings, nil
}

// mergeRecheckDelay is how long Merge waits before it retries a merge that Gitea refused while
// it checks whether the pull request can be merged.
var mergeRecheckDelay = 2 * time.Second

// Merge merges the pull request's head opts.HeadSHA, else its current head, with the selected
// strategy. When Gitea refuses the merge, the error names the reason derived from the pull
// request, since Gitea reports conflicts only as "try again later". Gitea answers the same while
// it checks the pull request again after its base branch moved, so such a refusal of an open
// pull request is retried once after a short delay.
func (s Pulls) Merge(ctx context.Context, n int64, opts MergeOptions) error {
	pull, err := s.get(ctx, n)
	if err != nil {
		return err
	}
	option := gitea.MergePullRequestOption{
		Style: gitea.MergeStyle(opts.Method), Title: opts.Title, Message: opts.Message, DeleteBranchAfterMerge: &opts.DeleteBranch,
		MergeWhenChecksSucceed: opts.Auto, MergeCommitID: opts.Commit,
	}
	if option.HeadCommitId = opts.HeadSHA; option.HeadCommitId == "" && pull.Head != nil {
		option.HeadCommitId = pull.Head.Sha
	}
	merge := func() error {
		return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
			_, resp, err := c.PullRequests.MergePullRequest(ctx, owner, name, n, option)
			return resp, err
		})
	}
	open := !pull.HasMerged && pull.State != gitea.StateClosed && !pull.Draft
	err = merge()
	later := func() bool {
		refused, ok := errors.AsType[*transport.APIError](err)
		return ok && refused.Status == http.StatusMethodNotAllowed && strings.Contains(strings.ToLower(refused.Message), "try again later")
	}
	if open && later() {
		if err := sleepCtx(ctx, mergeRecheckDelay); err != nil {
			return err
		}
		err = merge()
	}
	key := "#" + strconv.FormatInt(n, 10)
	refused, ok := errors.AsType[*transport.APIError](err)
	if !ok || refused.Status != http.StatusMethodNotAllowed {
		return Classify(err, "pull request", key, s.Repository)
	}
	var reason string
	switch {
	case pull.HasMerged:
		reason = "has already been merged"
	case pull.State == gitea.StateClosed:
		reason = "is closed"
	case pull.Draft:
		reason = "is a draft; mark it ready for review first"
	case later() && !pull.Mergeable:
		reason = "has conflicting files, or Gitea is still checking whether it can be merged; retry in a moment"
	case later():
		reason = "cannot be merged yet: Gitea is still checking whether it can be merged; retry in a moment"
	case !pull.Mergeable && refused.Message == "":
		reason = "has conflicting files or is otherwise not mergeable"
	case refused.Message != "":
		reason = "cannot be merged" // Gitea's message, such as "Does not have enough approvals", says why
	default:
		return Classify(err, "pull request", key, s.Repository)
	}
	return fmt.Errorf("pull request #%d %s: %w", n, reason, err)
}

// pullTemplatePaths lists the Markdown pull request templates Gitea reads, in its priority order.
var pullTemplatePaths = []string{
	"PULL_REQUEST_TEMPLATE.md", "pull_request_template.md",
	".gitea/PULL_REQUEST_TEMPLATE.md", ".gitea/pull_request_template.md",
	".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md",
}

const maxPullTemplateBytes = 1 << 20

// Template returns the first pull request template found at ref, or "" when the repository has none.
// All paths are probed concurrently; the result is decided in priority order, so a failure is
// reported only when every higher-priority path is missing.
func (s Pulls) Template(ctx context.Context, ref string) (string, error) {
	type probe struct {
		content string
		missing bool
		err     error
	}
	probes := make([]probe, len(pullTemplatePaths))
	var wg sync.WaitGroup
	for i, path := range pullTemplatePaths {
		wg.Go(func() {
			probes[i].content, probes[i].missing, probes[i].err = s.templateAt(ctx, ref, path)
		})
	}
	wg.Wait()
	for _, p := range probes {
		if !p.missing {
			return p.content, p.err
		}
	}
	return "", nil
}

// templateAt reads one template path; missing reports a 404.
func (s Pulls) templateAt(ctx context.Context, ref, path string) (content string, missing bool, err error) {
	file, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (io.ReadCloser, *gitea.Response, error) {
		return c.Repositories.GetFileReader(ctx, owner, name, ref, path)
	})
	if err != nil {
		if IsNotFound(err) {
			return "", true, nil
		}
		return "", false, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPullTemplateBytes))
	_ = file.Close()
	return string(data), false, err
}
