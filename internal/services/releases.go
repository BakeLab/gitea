// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

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

// ListPage lists a page of releases, with whether more follow and how many there are.
func (s Releases) ListPage(ctx context.Context, limit, page int) (Page[Release], error) {
	window, err := listPage(limit, page, func(limit, page int) ([]*gitea.Release, http.Header, error) {
		var headers http.Header
		releases, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Release, *gitea.Response, error) {
			releases, resp, err := c.Releases.ListReleases(ctx, owner, repo, gitea.ListReleasesOptions{ListOptions: listOptions(limit, page)})
			headers = headerOf(resp)
			return releases, resp, err
		})
		return releases, headers, err
	})
	if err != nil {
		return Page[Release]{}, err
	}
	return convertPage(window, releaseFrom), nil
}

// View retrieves a release by its tag, including numeric tags.
func (s Releases) View(ctx context.Context, tag string) (Release, error) {
	item, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.GetReleaseByTag(ctx, owner, repo, tag)
	})
	return item, Classify(err, "release", tag, s.Repository)
}

// Latest retrieves the most recent release that is neither a draft nor a prerelease.
func (s Releases) Latest(ctx context.Context) (Release, error) {
	release, err := repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.GetLatestRelease(ctx, owner, repo)
	})
	return release, Classify(err, "latest release", "", s.Repository)
}

// Archive opens the source archive of the repository at ref; format is zip or tar.gz.
func (s Releases) Archive(ctx context.Context, ref, format string) (io.ReadCloser, error) {
	return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (io.ReadCloser, *gitea.Response, error) {
		return c.Repositories.GetArchiveReader(ctx, owner, repo, ref, gitea.ArchiveType("."+format))
	})
}

// Create creates a release and lets Gitea create a missing tag at the supplied target.
func (s Releases) Create(ctx context.Context, opts ReleaseOptions) (Release, error) {
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

// Edit changes only supplied release fields; Gitea ignores empty text fields.
func (s Releases) Edit(ctx context.Context, id int64, opts ReleaseOptions) (Release, error) {
	return repoConvert(ctx, s.Client, s.Repository, releaseFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Release, *gitea.Response, error) {
		return c.Releases.EditRelease(ctx, owner, repo, id, gitea.EditReleaseOption{
			TagName: deref(opts.Tag), Target: deref(opts.Target), Title: deref(opts.Title), Note: deref(opts.Body),
			IsDraft: opts.Draft, IsPrerelease: opts.Prerelease,
		})
	})
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

// Assets applies CLI pagination to Gitea's unpaginated attachment collection.
func (s Releases) Assets(ctx context.Context, id int64, limit, page int) ([]ReleaseAsset, error) {
	items, err := s.AllAssets(ctx, id)
	if err != nil {
		return nil, err
	}
	return pageOf(items, limit, page)
}

// AllAssets retrieves the full collection; this endpoint has no server pagination.
func (s Releases) AllAssets(ctx context.Context, id int64) ([]ReleaseAsset, error) {
	attachments, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.Attachment, *gitea.Response, error) {
		return c.Releases.ListReleaseAttachments(ctx, owner, repo, id, gitea.ListReleaseAttachmentsOptions{Page: -1})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(attachments, assetFrom), nil
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
