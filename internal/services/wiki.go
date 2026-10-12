// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
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

// wikiPageSize is the page size for reading the whole wiki; Gitea replaces a limit of 1 with its
// default page size.
const wikiPageSize = 50

// ListPage lists a page of wiki pages, with whether more follow and how many there are. It reads
// the whole page list and pages it on the client, because the server pages it inconsistently.
func (s Wiki) ListPage(ctx context.Context, limit, page int) (Page[WikiPage], error) {
	all, err := listAll(func(page int) ([]*gitea.WikiPageMetaData, error) {
		return repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.WikiPageMetaData, *gitea.Response, error) {
			return c.Wiki.ListPages(ctx, owner, repo, gitea.ListWikiPagesOptions{ListOptions: listOptions(wikiPageSize, page)})
		})
	})
	if err != nil {
		if !IsNotFound(err) {
			return Page[WikiPage]{}, err
		}
		// An uninitialized wiki returns 404. Verify repository visibility and
		// wiki configuration before treating that response as an empty list.
		repository, checkErr := (Repositories{Client: s.Client}).get(ctx, s.Repository)
		if checkErr != nil {
			return Page[WikiPage]{}, checkErr
		}
		if !repository.HasWiki {
			return Page[WikiPage]{}, err
		}
		return Page[WikiPage]{Items: []WikiPage{}, Counted: true}, nil
	}
	items, err := pageOf(convertAll(all, func(item *gitea.WikiPageMetaData) WikiPage {
		return wikiPageFrom(item.Title, item.HTMLURL, item.LastCommit)
	}), limit, page)
	return Page[WikiPage]{Items: items, More: page*limit < len(all), Total: len(all), Counted: true}, err
}

// View retrieves and decodes a wiki page.
func (s Wiki) View(ctx context.Context, name string) (WikiPage, error) {
	item, err := s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.GetPage(ctx, owner, repo, name)
	})
	return item, Classify(err, "wiki page", strconv.Quote(name), s.Repository)
}

func wikiOptions(opts WikiOptions) gitea.CreateWikiPageOptions {
	return gitea.CreateWikiPageOptions{Title: opts.Title, ContentBase64: base64.StdEncoding.EncodeToString([]byte(opts.Body)), Message: opts.Message}
}

// Create creates one wiki page, initializing the wiki if necessary.
func (s Wiki) Create(ctx context.Context, opts WikiOptions) (WikiPage, error) {
	item, err := s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.CreatePage(ctx, owner, repo, wikiOptions(opts))
	})
	return item, Classify(err, "wiki page", strconv.Quote(opts.Title), s.Repository)
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
	item, err := s.page(ctx, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.WikiPage, *gitea.Response, error) {
		return c.Wiki.EditPage(ctx, owner, repo, name, wikiOptions(opts))
	})
	return item, Classify(err, "wiki page", strconv.Quote(name), s.Repository)
}

// Delete removes a named wiki page.
func (s Wiki) Delete(ctx context.Context, name string) error {
	err := repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		return c.Wiki.DeletePage(ctx, owner, repo, name)
	})
	return Classify(err, "wiki page", strconv.Quote(name), s.Repository)
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
