// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Release describes a repository release.
type Release struct {
	ID          int64          `json:"id"`
	Tag         string         `json:"tag"`
	Target      string         `json:"target"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	URL         string         `json:"url"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Author      string         `json:"author"`
	Assets      []ReleaseAsset `json:"assets"`
	CreatedAt   string         `json:"created_at"`
	PublishedAt *string        `json:"published_at"`
}

func releaseFrom(release *gitea.Release) Release {
	return Release{
		ID: release.ID, Tag: release.TagName, Target: release.Target, Title: release.Title, Body: release.Note, URL: release.HTMLURL,
		Draft: release.IsDraft, Prerelease: release.IsPrerelease, Author: login(release.Publisher), Assets: convertAll(release.Attachments, assetFrom),
		CreatedAt: timestamp(release.CreatedAt), PublishedAt: optionalTimestamp(&release.PublishedAt),
	}
}

// When is when a release was published, or created for a draft.
func (r Release) When() string {
	if r.PublishedAt != nil && !r.Draft {
		return *r.PublishedAt
	}
	return r.CreatedAt
}

// ReleaseOptions distinguishes omitted fields from explicit empty or false values.
type ReleaseOptions struct {
	Tag        *string `json:"tag_name,omitempty"`
	Target     *string `json:"target_commitish,omitempty"`
	Title      *string `json:"name,omitempty"`
	Body       *string `json:"body,omitempty"`
	Draft      *bool   `json:"draft,omitempty"`
	Prerelease *bool   `json:"prerelease,omitempty"`
}

// Releases implements repository release workflows.
type Releases struct {
	Client     *transport.Client
	Repository string
}

func (s Releases) endpoint() string { return "repos/" + s.Repository + "/releases" }

// ReleaseListOptions narrows a listing of releases.
type ReleaseListOptions struct {
	// ExcludeDrafts and ExcludePrereleases leave those out, which Gitea does itself.
	ExcludeDrafts, ExcludePrereleases bool
	// Order is "asc" or "desc" to sort the releases by when they were made, oldest or newest first, or ""
	// for the order of Gitea, the newest first. Gitea sorts no other way, so asc reads every
	// release and sorts them here.
	Order string
}

func (o ReleaseListOptions) filters(list gitea.ListOptions) gitea.ListReleasesOptions {
	options := gitea.ListReleasesOptions{ListOptions: list}
	if o.ExcludeDrafts {
		options.IsDraft = new(false)
	}
	if o.ExcludePrereleases {
		options.IsPreRelease = new(false)
	}
	return options
}

// ListPage lists a page of releases, with whether more follow and how many there are.
func (s Releases) ListPage(ctx context.Context, limit, page int, opts ReleaseListOptions) (Page[Release], error) {
	if opts.Order != "" {
		return s.listSorted(ctx, limit, page, opts)
	}
	window, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]*gitea.Release, http.Header, error) {
		var headers http.Header
		releases, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Release, *gitea.Response, error) {
			releases, resp, err := c.Releases.ListReleases(ctx, owner, repo, opts.filters(listOptions(limit, page)))
			headers = headerOf(resp)
			return releases, resp, err
		})
		return releases, headers, err
	})
	if err != nil {
		return Page[Release]{}, Classify(err, "repository", s.Repository, "")
	}
	return convertPage(window, releaseFrom), nil
}

// listSorted reads every release, sorts them by when they were published and cuts the window of limit at page.
func (s Releases) listSorted(ctx context.Context, limit, page int, opts ReleaseListOptions) (Page[Release], error) {
	unordered := opts
	unordered.Order = ""
	releases, err := listEvery(100, func(limit, page int) (Page[Release], error) { return s.ListPage(ctx, limit, page, unordered) })
	if err != nil {
		return Page[Release]{}, err
	}
	// created_at of a published release is the date of its commit, and migrated releases get their IDs in any order.
	when := func(r Release) time.Time {
		parsed, _ := time.Parse(time.RFC3339Nano, r.When())
		return parsed
	}
	slices.SortFunc(releases, func(x, y Release) int { return cmp.Or(when(x).Compare(when(y)), cmp.Compare(x.ID, y.ID)) })
	if opts.Order == "desc" {
		slices.Reverse(releases)
	}
	return PageOf(releases, limit, page)
}

// View retrieves a release by its tag, including numeric tags.
func (s Releases) View(ctx context.Context, tag string) (Release, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.GetReleaseByTag(ctx, owner, repo, tag)
	})
	return item, repositoryItemError(ctx, s.Client, s.Repository, err, "release", tag)
}

// Latest retrieves the most recent release that is neither a draft nor a prerelease.
func (s Releases) Latest(ctx context.Context) (Release, error) {
	release, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.GetLatestRelease(ctx, owner, repo)
	})
	return release, repositoryItemError(ctx, s.Client, s.Repository, err, "latest release", "")
}

// Archive opens the source archive of the repository at ref; format is zip or tar.gz.
func (s Releases) Archive(ctx context.Context, ref, format string) (io.ReadCloser, error) {
	body, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (io.ReadCloser, *gitea.Response, error) {
		return c.Repositories.GetArchiveReader(ctx, owner, repo, ref, gitea.ArchiveType("."+format))
	})
	// Gitea answers "unrecognized repository reference" for a ref that does not exist.
	return body, Classify(err, "ref", ref, s.Repository)
}

// Create creates a release and lets Gitea create a missing tag at the supplied target.
func (s Releases) Create(ctx context.Context, opts ReleaseOptions) (Release, error) {
	// Gitea resolves the target only when it creates the tag, which it does not for a draft.
	if target := deref(opts.Target); target != "" && deref(opts.Draft) {
		if err := s.requireTarget(ctx, target); err != nil {
			return Release{}, err
		}
	}
	item, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.CreateRelease(ctx, owner, repo, gitea.CreateReleaseOption{
			TagName: deref(opts.Tag), Target: deref(opts.Target), Title: deref(opts.Title), Note: deref(opts.Body),
			IsDraft: deref(opts.Draft), IsPrerelease: deref(opts.Prerelease),
		})
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusNotFound && strings.Contains(apiErr.Message, "target") {
		// The release does not exist yet, so a 404 here names the unresolvable target, not the release.
		return item, &NotFoundError{Kind: "target", Key: strconv.Quote(deref(opts.Target)), Scope: s.Repository, Err: err}
	}
	if empty := emptyRepository(err, s.Repository, "first"); empty != nil {
		return item, empty
	}
	if IsNotFound(err) {
		// Any other 404 means the repository is missing or invisible.
		return item, Classify(err, "repository", s.Repository, "")
	}
	return item, Classify(err, "release", deref(opts.Tag), s.Repository)
}

// TagExists reports whether the repository on the server has the tag.
func (s Releases) TagExists(ctx context.Context, tag string) (bool, error) {
	_, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Tag, *gitea.Response, error) {
		return c.Repositories.GetTag(ctx, owner, repo, tag)
	})
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// TagCommit returns the SHA of the commit that tag points at.
func (s Releases) TagCommit(ctx context.Context, tag string) (string, error) {
	item, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Tag, *gitea.Response, error) {
		return c.Repositories.GetTag(ctx, owner, repo, tag)
	})
	if err != nil {
		return "", Classify(err, "tag", tag, s.Repository)
	}
	if item.Commit == nil {
		return "", NotFound("commit of tag", tag, s.Repository)
	}
	return item.Commit.SHA, nil
}

// Edit changes only supplied release fields of current; Gitea ignores empty text fields.
func (s Releases) Edit(ctx context.Context, current Release, opts ReleaseOptions) (Release, error) {
	if target := deref(opts.Target); target != "" {
		if err := s.requireTarget(ctx, target); err != nil {
			return Release{}, err
		}
	}
	// Gitea fails with HTTP 500 for a tag another release has, and tells only administrators why.
	if tag := deref(opts.Tag); tag != "" && tag != current.Tag {
		_, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
			return c.Releases.GetReleaseByTag(ctx, owner, repo, tag)
		})
		if err == nil {
			return Release{}, &ConflictError{Reason: ReasonExists, Kind: "release", Key: tag, Scope: s.Repository}
		}
		if !IsNotFound(err) {
			return Release{}, Classify(err, "release", tag, s.Repository)
		}
	}
	item, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.EditRelease(ctx, owner, repo, current.ID, gitea.EditReleaseOption{
			TagName: deref(opts.Tag), Target: deref(opts.Target), Title: deref(opts.Title), Note: deref(opts.Body),
			IsDraft: opts.Draft, IsPrerelease: opts.Prerelease,
		})
	})
	// Publishing a draft whose target does not exist fails with HTTP 500, which tells only
	// administrators why. A tag that exists already is published without the target, so the
	// target is only checked after the failure.
	if missing := s.missingTarget(err); missing != nil {
		return item, missing
	}
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status >= http.StatusInternalServerError && current.Draft && opts.Draft != nil && !*opts.Draft && opts.Target == nil && current.Target != "" {
		if missing, ok := errors.AsType[*NotFoundError](s.requireTarget(ctx, current.Target)); ok && missing.Kind == "target" {
			missing.Err = err
			return item, missing
		}
	}
	return item, err
}

// missingObject matches Gitea's error for a branch, tag or commit that does not exist.
var missingObject = regexp.MustCompile(`object does not exist \[id: ([^,\]]*)`)

// requireTarget fails when target names no branch, tag or commit of the repository.
func (s Releases) requireTarget(ctx context.Context, target string) error {
	return requireRef(ctx, s.Client, s.Repository, target, "target")
}

// requireRef fails with a *NotFoundError of kind when ref names no branch, tag or commit of repo.
func requireRef(ctx context.Context, client *transport.Client, repo, ref, kind string) error {
	_, err := repoCall(ctx, client, repo, func(ctx context.Context, c *gitea.Client, owner, name string) ([]*gitea.Commit, *gitea.Response, error) {
		return c.Repositories.ListRepoCommits(ctx, owner, name, gitea.ListCommitOptions{ListOptions: gitea.ListOptions{Page: 1, PageSize: 1}, SHA: ref})
	})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && missingObject.MatchString(apiErr.Message) {
		return &NotFoundError{Kind: kind, Key: strconv.Quote(ref), Scope: repo, Err: err}
	}
	if empty := emptyRepository(err, repo, "first"); empty != nil {
		return empty
	}
	return Classify(err, "repository", repo, "")
}

// missingTarget returns the target that err says does not exist as a *NotFoundError, or nil.
func (s Releases) missingTarget(err error) error {
	apiErr, ok := errors.AsType[*transport.APIError](err)
	if !ok {
		return nil
	}
	if match := missingObject.FindStringSubmatch(apiErr.Message); match != nil {
		return &NotFoundError{Kind: "target", Key: strconv.Quote(match[1]), Scope: s.Repository, Err: err}
	}
	return nil
}

// Delete removes a release and optionally its tag; partial failures identify the completed step.
func (s Releases) Delete(ctx context.Context, item Release, deleteTag bool) error {
	if err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Releases.DeleteRelease(ctx, owner, repo, item.ID)
	}); err != nil {
		return err
	}
	if deleteTag {
		if err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
			return c.Repositories.DeleteTag(ctx, owner, repo, item.Tag)
		}); err != nil {
			return fmt.Errorf("release deleted, but tag deletion failed: %w", err)
		}
	}
	return nil
}

// DeleteAsset removes one asset from the selected release.
func (s Releases) DeleteAsset(ctx context.Context, id, asset int64) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Releases.DeleteReleaseAttachment(ctx, owner, repo, id, asset)
	})
}

// RenameAsset changes only an attachment's display name.
func (s Releases) RenameAsset(ctx context.Context, id, asset int64, name string) (ReleaseAsset, error) {
	return repoConvert(ctx, s.Client, s.Repository, assetFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Releases.EditReleaseAttachment(ctx, owner, repo, id, asset, gitea.EditAttachmentOptions{Name: name})
	})
}

// UploadAsset streams a multipart upload without buffering the asset in memory.
func (s Releases) UploadAsset(ctx context.Context, id int64, name string, input io.Reader) (ReleaseAsset, error) {
	return uploadAttachment(ctx, s.Client, fmt.Sprintf("%s/%d/assets", s.endpoint(), id), name, input)
}

// DownloadAsset opens a repository-validated attachment stream at its direct instance URL.
func (s Releases) DownloadAsset(ctx context.Context, id, asset int64) (io.ReadCloser, error) {
	attachment, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Attachment, *gitea.Response, error) {
		return c.Releases.GetReleaseAttachment(ctx, owner, repo, id, asset)
	})
	if err != nil {
		return nil, err
	}
	return downloadAttachment(ctx, s.Client, attachment)
}
