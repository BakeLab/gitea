// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// WikiPage exposes decoded page content and revision metadata. Gitea lists pages without their
// content, so the JSON of a listed page leaves out body, commit_count, sidebar and footer rather
// than report them empty.
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
	// listed marks a page of a listing, which carries no content.
	listed bool
}

// MarshalJSON leaves the content fields out of a listed page.
func (p WikiPage) MarshalJSON() ([]byte, error) {
	type page WikiPage
	if !p.listed {
		return json.Marshal(page(p))
	}
	return json.Marshal(struct {
		Title     string `json:"title"`
		URL       string `json:"url"`
		SHA       string `json:"sha"`
		Author    string `json:"author"`
		UpdatedAt string `json:"updated_at"`
	}{p.Title, p.URL, p.SHA, p.Author, p.UpdatedAt})
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
	body, bodyErr := base64.StdEncoding.DecodeString(page.ContentBase64)
	sidebar, sidebarErr := base64.StdEncoding.DecodeString(page.Sidebar)
	footer, footerErr := base64.StdEncoding.DecodeString(page.Footer)
	if err := errors.Join(bodyErr, sidebarErr, footerErr); err != nil {
		return WikiPage{}, fmt.Errorf("decode wiki content: %w", err)
	}
	result := wikiPageFrom(page.Title, page.HTMLURL, page.LastCommit)
	result.Body, result.CommitCount, result.Sidebar, result.Footer = string(body), page.CommitCount, string(sidebar), string(footer)
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

// ListPage lists a page of wiki pages, with whether more follow and, when the whole wiki was read,
// how many there are. Gitea windows the wiki's files, not its pages: a window holds fewer pages
// than its size, or none, while later windows still hold pages. The windows are read in order
// while the server links a next one, until one page past the requested page is found.
func (s Wiki) ListPage(ctx context.Context, limit, page int) (Page[WikiPage], error) {
	if limit < 1 || page < 1 {
		return Page[WikiPage]{}, fmt.Errorf("limit and page must be positive")
	}
	all, wanted := []WikiPage{}, page*limit+1
	for window := 1; ; window++ {
		if window > maxListPages {
			return Page[WikiPage]{}, fmt.Errorf("listing exceeded %d pages", maxListPages)
		}
		var headers http.Header
		items, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) ([]*gitea.WikiPageMetaData, *gitea.Response, error) {
			items, resp, err := c.Wiki.ListPages(ctx, owner, repo, gitea.ListWikiPagesOptions{ListOptions: listOptions(wikiPageSize, window)})
			headers = headerOf(resp)
			return items, resp, err
		})
		if err != nil {
			if window > 1 || !IsNotFound(err) {
				return Page[WikiPage]{}, err
			}
			// An uninitialized wiki returns 404. Verify repository visibility and
			// wiki configuration before treating that response as an empty list.
			if err := s.disabled(ctx, err); err != nil {
				return Page[WikiPage]{}, err
			}
			return Page[WikiPage]{Items: []WikiPage{}, Counted: true}, nil
		}
		all = append(all, convertAll(items, func(item *gitea.WikiPageMetaData) WikiPage {
			listed := wikiPageFrom(item.Title, item.HTMLURL, item.LastCommit)
			listed.listed = true
			return listed
		})...)
		if next, _, _ := pageHints(headers); !next {
			break
		}
		if len(all) >= wanted {
			items, err := pageOf(all, limit, page)
			return Page[WikiPage]{Items: items, More: true}, err
		}
	}
	items, err := pageOf(all, limit, page)
	return Page[WikiPage]{Items: items, More: page*limit < len(all), Total: len(all), Counted: true}, err
}

// disabled explains a 404 from the wiki API. Gitea answers a bare 404 when the wiki is disabled or
// the repository is missing, which says nothing about the page itself. It returns nil for any other
// error, and the repository lookup's error when the repository cannot be read.
func (s Wiki) disabled(ctx context.Context, err error) error {
	if !IsNotFound(err) {
		return nil
	}
	repository, checkErr := (Repositories{Client: s.Client}).get(ctx, s.Repository)
	if checkErr != nil {
		return checkErr
	}
	if !repository.HasWiki {
		return fmt.Errorf("the wiki of %s is disabled; turn it on with: gt repo edit --enable-wiki -R %s", s.Repository, s.Repository)
	}
	return nil
}

// View retrieves and decodes a wiki page.
func (s Wiki) View(ctx context.Context, name string) (WikiPage, error) {
	item, err := s.lookup(ctx, name)
	if err := s.disabled(ctx, err); err != nil {
		return WikiPage{}, err
	}
	return item, err
}

// wikiPath is the page path that Gitea's wiki API takes for the page titled title. Gitea reads a
// path ending in ".-" as the title itself, and any other path as a title with its spaces written
// as "-", so like Gitea's own links it marks titles that contain "-" or end in ".md" with ".-".
func wikiPath(title string) string {
	if !strings.Contains(title, "-") && !strings.HasSuffix(title, ".md") {
		return title
	}
	return title + ".-"
}

// wikiEndpoint is the API endpoint of the wiki page at path, below "wiki/page" or "wiki/revisions"
// as kind says.
// TODO(sdk): WikiService escapes the page path with url.PathEscape, which keeps "+", and Gitea
// reads a "+" in the path as a space, so a title such as "C++" cannot be addressed through it.
func (s Wiki) wikiEndpoint(kind, path string) (string, error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return "", err
	}
	escaped := strings.ReplaceAll(url.PathEscape(path), "+", "%2B")
	return "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/wiki/" + kind + "/" + escaped, nil
}

// pageAt sends method with payload, when not nil, to the page at path and decodes the page it
// answers with.
func (s Wiki) pageAt(ctx context.Context, method, path string, payload any) (WikiPage, error) {
	endpoint, err := s.wikiEndpoint("page", path)
	if err != nil {
		return WikiPage{}, err
	}
	var page gitea.WikiPage
	if err := sendJSON(ctx, s.Client, method, endpoint, payload, &page); err != nil {
		return WikiPage{}, err
	}
	return wikiContentFrom(&page)
}

// atPage calls op with the path of the page titled name and, when there is no such page, with
// name itself as the path, such as the Getting-Started of a page URL.
func atPage[T any](name string, op func(path string) (T, error)) (T, error) {
	result, err := op(wikiPath(name))
	if IsNotFound(err) && wikiPath(name) != name {
		return op(name)
	}
	return result, err
}

// lookup is View without the check for a disabled wiki.
func (s Wiki) lookup(ctx context.Context, name string) (WikiPage, error) {
	name = strings.TrimSpace(name)
	item, err := atPage(name, func(path string) (WikiPage, error) {
		return s.pageAt(ctx, http.MethodGet, path, nil)
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
	if err := s.disabled(ctx, err); err != nil {
		return WikiPage{}, err
	}
	// Gitea trims the title, and matches an existing page by what is left.
	return item, Classify(err, "wiki page", strconv.Quote(strings.TrimSpace(opts.Title)), s.Repository)
}

// Edit replaces page content and optionally changes its title. It reports whether the title
// changed. Gitea's edit creates a page that does not exist and overwrites one the new title
// names, so both are checked first.
func (s Wiki) Edit(ctx context.Context, name string, opts WikiOptions) (WikiPage, bool, error) {
	name, opts.Title = strings.TrimSpace(name), strings.TrimSpace(opts.Title)
	current, err := s.View(ctx, name)
	if err != nil {
		return WikiPage{}, false, err
	}
	renamed := opts.Title != "" && opts.Title != current.Title
	if opts.Title == "" {
		opts.Title = current.Title
	}
	if renamed {
		existing, err := s.lookup(ctx, opts.Title)
		if err == nil && existing.Title != current.Title {
			return WikiPage{}, false, &ConflictError{Reason: ReasonExists, Kind: "wiki page", Key: strconv.Quote(opts.Title), Scope: s.Repository}
		}
		if err != nil && !IsNotFound(err) {
			return WikiPage{}, false, err
		}
	}
	item, err := s.pageAt(ctx, http.MethodPatch, wikiPath(current.Title), wikiOptions(opts))
	return item, renamed, Classify(err, "wiki page", strconv.Quote(name), s.Repository)
}

// Delete removes a named wiki page.
func (s Wiki) Delete(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	_, err := atPage(name, func(path string) (struct{}, error) {
		endpoint, err := s.wikiEndpoint("page", path)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, sendJSON(ctx, s.Client, http.MethodDelete, endpoint, nil, nil)
	})
	if err := s.disabled(ctx, err); err != nil {
		return err
	}
	// Gitea fails with a server error to delete from a wiki that was never created.
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status >= http.StatusInternalServerError {
		if _, lookupErr := s.lookup(ctx, name); IsNotFound(lookupErr) {
			return lookupErr
		}
	}
	return Classify(err, "wiki page", strconv.Quote(name), s.Repository)
}

// wikiRevisionPageSize is Gitea's default number of wiki commits per page ([git] COMMITS_RANGE_SIZE).
const wikiRevisionPageSize = 50

// Revisions returns one server-paginated page of wiki commits and the total the server counted.
func (s Wiki) Revisions(ctx context.Context, name string, page int) (Page[WikiRevision], error) {
	name = strings.TrimSpace(name)
	revisions, err := atPage(name, func(path string) (*gitea.WikiCommitList, error) {
		endpoint, err := s.wikiEndpoint("revisions", path)
		if err != nil {
			return nil, err
		}
		revisions := new(gitea.WikiCommitList)
		return revisions, getJSON(ctx, s.Client, endpoint+"?page="+strconv.Itoa(page), revisions)
	})
	if err := s.disabled(ctx, err); err != nil {
		return Page[WikiRevision]{}, err
	}
	if err != nil {
		return Page[WikiRevision]{}, Classify(err, "wiki page", strconv.Quote(name), s.Repository)
	}
	items := convertAll(revisions.WikiCommits, wikiRevisionFrom)
	total := int(revisions.Count)
	return Page[WikiRevision]{Items: items, Total: total, Counted: true, More: (page-1)*wikiRevisionPageSize+len(items) < total}, nil
}
