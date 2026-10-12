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
	// MergedBy, Reviewers and ReviewerTeams are only in responses for one pull request; lists
	// leave them out.
	MergedBy  string `json:"merged_by"`
	Mergeable bool   `json:"mergeable"`
	Draft     bool   `json:"draft"`
	// ReviewDecision summarizes the reviews like gh: approved, changes_requested, review_required
	// or "". Only commands that read the reviews fill it.
	ReviewDecision    string   `json:"review_decision"`
	Base              string   `json:"base"`
	Head              string   `json:"head"`
	HeadSHA           string   `json:"head_sha"`
	HeadRepo          string   `json:"head_repo,omitempty"`
	HeadRemoteDeleted bool     `json:"head_remote_deleted"`
	HeadCanPush       bool     `json:"-"`
	HeadCloneURL      string   `json:"-"`
	HeadSSHURL        string   `json:"-"`
	Reviewers         []string `json:"reviewers"`
	ReviewerTeams     []string `json:"reviewer_teams"`
	Comments          int64    `json:"comments"`
}

func pullHeadBranch(number int64, ref, label string) (branch string, deleted bool) {
	if ref == fmt.Sprintf("refs/pull/%d/head", number) {
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
		Mergeable: pull.Mergeable, Draft: pull.Draft,
		Base: base.Ref, Head: headBranch, HeadSHA: head.Sha, HeadRemoteDeleted: remoteDeleted,
		Reviewers: convertAll(pull.RequestedReviewers, login), Comments: int64(pull.Comments),
		ReviewerTeams: convertAll(pull.RequestedReviewersTeams, func(team *gitea.Team) string { return team.Name }),
	}
	repo := head.Repository
	if remoteDeleted || repo == nil {
		repo = base.Repository
	}
	if repo != nil {
		result.HeadRepo, result.HeadCloneURL, result.HeadSSHURL = repo.FullName, repo.CloneURL, repo.SSHURL
		if repo.Permissions != nil {
			result.HeadCanPush = repo.Permissions.Push
		}
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
	// Draft, Head (BRANCH or OWNER:BRANCH), Assignee, Search (text in the title or body) and the
	// merged state have no server filter; List applies them to every pull request it reads.
	Draft                  bool
	Head, Assignee, Search string
}

// filtered reports whether a filter needs every pull request read.
func (o PullListOptions) filtered() bool {
	return o.State == "merged" || o.Draft || o.Head != "" || o.Assignee != "" || o.Search != ""
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
	if options.Limit <= 0 || options.Page <= 0 {
		return Page[Pull]{}, errors.New("limit and page must be positive")
	}
	query := options
	if query.State == "merged" {
		query.State = "closed"
	}
	matching, wanted := []Pull{}, options.Page*options.Limit+1
	for page := 1; ; page++ {
		if page > maxListPages {
			return Page[Pull]{}, fmt.Errorf("listing exceeded %d pages", maxListPages)
		}
		query.Limit, query.Page = 50, page
		window, err := s.listPage(ctx, query)
		if err != nil {
			return Page[Pull]{}, err
		}
		for _, item := range window.Items {
			if options.matches(item) {
				matching = append(matching, item)
			}
		}
		if len(matching) >= wanted {
			items, err := pageOf(matching, options.Limit, options.Page)
			return Page[Pull]{Items: items, More: true}, err
		}
		if len(window.Items) == 0 || (!window.More && len(window.Items) < query.Limit) {
			break
		}
	}
	items, err := pageOf(matching, options.Limit, options.Page)
	if err != nil {
		return Page[Pull]{}, err
	}
	return Page[Pull]{Items: items, More: options.Page*options.Limit < len(matching), Total: len(matching), Counted: true}, nil
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
		return Page[Pull]{}, err
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

// ViewAll retrieves each of items again, a few at a time, for the fields only a single pull
// request carries, such as its reviewers.
func (s Pulls) ViewAll(ctx context.Context, items []Pull) ([]Pull, error) {
	viewed := make([]Pull, len(items))
	err := concurrently(len(items), func(i int) (err error) {
		viewed[i], err = s.View(ctx, items[i].Number)
		return err
	})
	return viewed, err
}

func (s Pulls) get(ctx context.Context, n int64) (*gitea.PullRequest, error) {
	item, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.GetPullRequest(ctx, owner, name, n)
	})
	return item, Classify(err, "pull request", "#"+strconv.FormatInt(n, 10), s.Repository)
}

// ListOpen returns every open pull request in the repository.
func (s Pulls) ListOpen(ctx context.Context) ([]Pull, error) {
	return listAll(func(page int) ([]Pull, error) {
		items, _, err := s.List(ctx, PullListOptions{State: "open", Limit: 50, Page: page})
		return items, err
	})
}

// Create opens a pull request.
func (s Pulls) Create(ctx context.Context, opts PullOptions) (Pull, error) {
	// TODO(sdk): CreatePullRequestOption lacks allow_maintainer_edit.
	var pull gitea.PullRequest
	if err := sendJSON(ctx, s.Client, http.MethodPost, "repos/"+s.Repository+"/pulls", opts, &pull); err != nil {
		return Pull{}, Classify(err, "pull request", "", s.Repository)
	}
	return pullFrom(&pull), nil
}

// Edit updates only explicitly supplied resource fields.
func (s Pulls) Edit(ctx context.Context, n int64, opts PullEdit) (Pull, error) {
	if opts.ReviewerChanges != nil {
		reviews, changes := Reviews{Client: s.Client, Repository: s.Repository}, opts.ReviewerChanges
		opts.ReviewerChanges = nil
		if opts == (PullEdit{}) {
			return reviews.ChangeRequests(ctx, n, *changes)
		}
		if err := reviews.changeRequests(ctx, n, *changes); err != nil {
			return Pull{}, err
		}
		item, err := s.Edit(ctx, n, opts)
		if err != nil {
			return Pull{}, fmt.Errorf("review requests were updated, but editing other pull request fields failed: %w", err)
		}
		return item, nil
	}
	issues := Issues{Client: s.Client, Repository: s.Repository}
	if opts.Changes != nil {
		resolved, err := issues.applyChanges(ctx, n, IssueEdit{Labels: opts.Labels, Assignees: opts.Assignees, Changes: opts.Changes})
		if err != nil {
			return Pull{}, err
		}
		opts.Labels, opts.Assignees, opts.Changes = resolved.Labels, resolved.Assignees, nil
	}
	if opts.Milestone != nil || opts.Labels != nil {
		// The PR endpoint ignores zero milestones. Its issue endpoint supports
		// clearing and lets common fields share the same mutation.
		_, err := issues.Edit(ctx, n, IssueEdit{
			Title: opts.Title, Body: opts.Body, Assignees: opts.Assignees, Milestone: opts.Milestone, Labels: opts.Labels,
			Deadline: opts.Deadline, RemoveDeadline: opts.RemoveDeadline,
		})
		if err != nil {
			return Pull{}, err
		}
		opts.Title, opts.Body, opts.Assignees, opts.Milestone = nil, nil, nil, nil
		opts.Deadline, opts.RemoveDeadline, opts.Labels = nil, nil, nil
		var result Pull
		if opts == (PullEdit{}) {
			result, err = s.View(ctx, n)
		} else {
			result, err = s.Edit(ctx, n, opts)
		}
		if err != nil {
			return Pull{}, fmt.Errorf("issue fields were updated, but completing the pull request edit failed: %w", err)
		}
		return result, nil
	}
	item, err := repoConvert(ctx, s.Client, s.Repository, pullFrom, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.PullRequest, *gitea.Response, error) {
		return c.PullRequests.EditPullRequest(ctx, owner, name, n, editPullOption(opts))
	})
	return item, Classify(err, "pull request", "#"+strconv.FormatInt(n, 10), s.Repository)
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
		return nil, err
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
	files, err := listAll(func(page int) ([]*gitea.ChangedFile, error) {
		return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.ChangedFile, *gitea.Response, error) {
			return c.PullRequests.ListPullRequestFiles(ctx, owner, name, n, gitea.ListPullRequestFilesOptions{ListOptions: listOptions(100, page)})
		})
	})
	if err != nil {
		return nil, err
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

// Merge merges the pull request's current head with the selected strategy; Gitea refuses the
// merge if the head moves meanwhile. When Gitea refuses the merge, the error names the reason
// derived from the pull request, since Gitea reports conflicts only as "try again later".
func (s Pulls) Merge(ctx context.Context, n int64, opts MergeOptions) error {
	pull, err := s.get(ctx, n)
	if err != nil {
		return err
	}
	option := gitea.MergePullRequestOption{
		Style: gitea.MergeStyle(opts.Method), Title: opts.Title, Message: opts.Message, DeleteBranchAfterMerge: &opts.DeleteBranch,
		MergeWhenChecksSucceed: opts.Auto,
	}
	if pull.Head != nil {
		option.HeadCommitId = pull.Head.Sha
	}
	err = repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (*gitea.Response, error) {
		_, resp, err := c.PullRequests.MergePullRequest(ctx, owner, name, n, option)
		return resp, err
	})
	key := "#" + strconv.FormatInt(n, 10)
	if refused, ok := errors.AsType[*transport.APIError](err); !ok || refused.Status != http.StatusMethodNotAllowed {
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
	case !pull.Mergeable:
		reason = "has conflicting files or is otherwise not mergeable"
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
func (s Pulls) Template(ctx context.Context, ref string) (string, error) {
	for _, path := range pullTemplatePaths {
		file, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, name string) (io.ReadCloser, *gitea.Response, error) {
			return c.Repositories.GetFileReader(ctx, owner, name, ref, path)
		})
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(file, maxPullTemplateBytes))
		_ = file.Close()
		return string(data), err
	}
	return "", nil
}
