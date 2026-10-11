// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	gitea "gitea.dev/sdk"
	"gitea.dev/tea/internal/transport"
)

// WikiPage exposes decoded page content and revision metadata.
type WikiPage struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	SHA         string `json:"sha"`
	Author      string `json:"author"`
	UpdatedAt   string `json:"updated_at"`
	CommitCount int64  `json:"commit_count"`
	Sidebar     string `json:"sidebar"`
	Footer      string `json:"footer"`
}

// WikiRevision represents a wiki commit without exposing author email addresses.
type WikiRevision struct {
	SHA       string `json:"sha"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
}

func wikiRevisionFrom(commit *gitea.WikiCommit) WikiRevision {
	result := WikiRevision{SHA: commit.ID, Message: commit.Message}
	if commit.Author != nil {
		result.Author = commit.Author.Name
		result.CreatedAt = commit.Author.Date
	}
	return result
}

func wikiPageFrom(title, url string, lastCommit *gitea.WikiCommit) WikiPage {
	result := WikiPage{Title: title, URL: url}
	if lastCommit != nil {
		revision := wikiRevisionFrom(lastCommit)
		result.SHA, result.Author, result.UpdatedAt = revision.SHA, revision.Author, revision.CreatedAt
	}
	return result
}

func wikiContentFrom(page *gitea.WikiPage) (WikiPage, error) {
	body, err := base64.StdEncoding.DecodeString(page.ContentBase64)
	if err != nil {
		return WikiPage{}, fmt.Errorf("decode wiki content: %w", err)
	}
	result := wikiPageFrom(page.Title, page.HTMLURL, page.LastCommit)
	result.Body, result.CommitCount, result.Sidebar, result.Footer = string(body), page.CommitCount, page.Sidebar, page.Footer
	return result, nil
}

// WikiOptions supplies title, decoded content and a commit message.
type WikiOptions struct{ Title, Body, Message string }

// Wiki implements repository wiki operations.
type Wiki struct {
	Client     *transport.Client
	Repository string
}

func (s Wiki) page(ctx context.Context, op repoOperation[*gitea.WikiPage]) (WikiPage, error) {
	page, err := repoCall(ctx, s.Client, s.Repository, op)
	if err != nil {
		return WikiPage{}, err
	}
	return wikiContentFrom(page)
}

// List returns a non-nil page of wiki metadata.
func (s Wiki) List(ctx context.Context, limit, page int) ([]WikiPage, error) {
	pages, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.WikiPageMetaData, *gitea.Response, error) {
		return c.Wiki.ListPages(ctx, owner, repo, gitea.ListWikiPagesOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		if apiError, ok := errors.AsType[*transport.APIError](err); !ok || apiError.Status != http.StatusNotFound {
			return nil, err
		}
		// An uninitialized wiki returns 404. Verify repository visibility and
		// wiki configuration before treating that response as an empty list.
		repository, checkErr := (Repositories{Client: s.Client}).get(ctx, s.Repository)
		if checkErr != nil {
			return nil, checkErr
		}
		if !repository.HasWiki {
			return nil, err
		}
		return []WikiPage{}, nil
	}
	return convertAll(pages, func(item *gitea.WikiPageMetaData) WikiPage {
		return wikiPageFrom(item.Title, item.HTMLURL, item.LastCommit)
	}), nil
}

// View retrieves and decodes a wiki page.
func (s Wiki) View(ctx context.Context, name string) (WikiPage, error) {
	return s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.GetPage(ctx, owner, repo, name)
	})
}

func wikiOptions(opts WikiOptions) gitea.CreateWikiPageOptions {
	return gitea.CreateWikiPageOptions{Title: opts.Title, ContentBase64: base64.StdEncoding.EncodeToString([]byte(opts.Body)), Message: opts.Message}
}

// Create creates one wiki page, initializing the wiki if necessary.
func (s Wiki) Create(ctx context.Context, opts WikiOptions) (WikiPage, error) {
	return s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.CreatePage(ctx, owner, repo, wikiOptions(opts))
	})
}

// Edit replaces page content and optionally changes its title.
func (s Wiki) Edit(ctx context.Context, name string, opts WikiOptions) (WikiPage, error) {
	if opts.Title == "" {
		current, err := s.View(ctx, name)
		if err != nil {
			return WikiPage{}, err
		}
		opts.Title = current.Title
	}
	return s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.EditPage(ctx, owner, repo, name, wikiOptions(opts))
	})
}

// Delete removes a named wiki page.
func (s Wiki) Delete(ctx context.Context, name string) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Wiki.DeletePage(ctx, owner, repo, name)
	})
}

// Revisions returns one server-paginated page of wiki commits.
func (s Wiki) Revisions(ctx context.Context, name string, page int) ([]WikiRevision, error) {
	revisions, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiCommitList, *gitea.Response, error) {
		return c.Wiki.GetPageRevisions(ctx, owner, repo, name, gitea.ListWikiPageRevisionsOptions{Page: page})
	})
	if err != nil {
		return nil, err
	}
	return convertAll(revisions.WikiCommits, wikiRevisionFrom), nil
}
