// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
)

// API provides the raw escape hatch while keeping credentials scoped to one instance.
type API struct{ Client *transport.Client }

// APIOptions describes a raw request and its bounded pagination policy.
type APIOptions struct {
	Endpoint string
	Method   string
	Body     io.Reader
	Headers  http.Header
	Paginate bool
	MaxPages int
}

// ErrMaxPages reports a paginated request that has more pages than APIOptions.MaxPages allows.
var ErrMaxPages = errors.New("API pagination exceeded the page limit")

func nextPageLinks(headers []string) ([]string, error) {
	result := []string{}
	for _, header := range headers {
		start := 0
		quoted, angle, escaped := false, false, false
		parts := []string{}
		for index, char := range header {
			if escaped {
				escaped = false
				continue
			}
			if quoted && char == '\\' {
				escaped = true
				continue
			}
			if char == '"' && !angle {
				quoted = !quoted
			}
			if !quoted {
				if char == '<' {
					angle = true
				}
				if char == '>' {
					angle = false
				}
				if char == ',' && !angle {
					parts = append(parts, header[start:index])
					start = index + 1
				}
			}
		}
		if quoted || angle {
			return nil, errors.New("API returned an invalid pagination Link header")
		}
		parts = append(parts, header[start:])
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			endpoint, parameters, ok := strings.Cut(part, ">")
			if !ok || !strings.HasPrefix(endpoint, "<") {
				return nil, errors.New("API returned an invalid pagination Link header")
			}
			_, params, err := mime.ParseMediaType("link" + parameters)
			if err != nil {
				return nil, errors.New("API returned invalid pagination parameters")
			}
			if slices.Contains(strings.Fields(params["rel"]), "next") {
				result = append(result, strings.TrimPrefix(endpoint, "<"))
			}
		}
	}
	return result, nil
}

// Visit delivers responses in order and closes each body after the visitor.
// A response outside 2xx is still visited, then ends the visit as an *transport.APIError.
// Pagination follows only scoped links and never retries a mutation.
func (s API) Visit(ctx context.Context, options APIOptions, visit func(*http.Response) error) error {
	if options.Paginate && (options.Method != http.MethodGet || options.Body != nil) {
		return errors.New("pagination requires a body-free GET request")
	}
	if options.MaxPages < 1 {
		options.MaxPages = 1000
	}
	endpoint := options.Endpoint
	seen := map[string]bool{}
	var unlinked unlinkedPages
	// probe is set for a page that unlinkedNext asked for, which can turn out to be past the end.
	// overLimit is set for a probe past MaxPages, which is asked for only to tell whether more pages
	// exist: one past the end does not count against the limit.
	probe, overLimit := false, false
	for page := 0; ; page++ {
		absolute, err := s.Client.URL(endpoint)
		if err != nil {
			return err
		}
		if seen[absolute] {
			return errors.New("API pagination link repeats an earlier page")
		}
		seen[absolute] = true
		current, _ := url.Parse(absolute)
		captureCtx, failure := transport.CaptureErrors(ctx)
		response, err := s.Client.Send(captureCtx, options.Method, endpoint, options.Body, options.Headers)
		if err != nil {
			return err
		}
		links := response.Header.Values("Link")
		body := &capped{limit: maxCountedPage}
		probed := probe && response.StatusCode >= 200 && response.StatusCode < 300
		if probed {
			// A page past the end of the list, which Gitea answers with null, an empty list or,
			// for a list that ignores the page asked for, the page before, is not visited.
			end, err := unlinked.past(response, body)
			if err != nil || end {
				_ = response.Body.Close()
				return err
			}
		}
		if overLimit {
			_ = response.Body.Close()
			return ErrMaxPages
		}
		if !probed && options.Paginate && response.StatusCode >= 200 && response.StatusCode < 300 && response.Header.Get("X-Total-Count") == "0" {
			if err := emptyList(response); err != nil {
				_ = response.Body.Close()
				return err
			}
		}
		if !probed && options.Paginate {
			response.Body = struct {
				io.Reader
				io.Closer
			}{io.TeeReader(response.Body, body), response.Body}
		}
		err = visit(response)
		closeErr := response.Body.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if captured := failure(); captured != nil {
				return captured // carries the server message even when the visitor wrote the body to a file
			}
			return &transport.APIError{Status: response.StatusCode, Method: options.Method, Endpoint: current.Path}
		}
		if !options.Paginate {
			return nil
		}
		nextLinks, err := nextPageLinks(links)
		if err != nil {
			return err
		}
		if len(nextLinks) > 1 {
			return errors.New("API returned multiple next-page links")
		}
		next := ""
		if len(nextLinks) == 1 {
			next = nextLinks[0]
		}
		probe = false
		if next == "" && len(links) == 0 {
			next = s.unlinkedNext(ctx, current, response.Header, body, &unlinked)
			probe = next != ""
		}
		if next == "" {
			return nil
		}
		if page+1 >= options.MaxPages {
			if !probe {
				return ErrMaxPages
			}
			overLimit = true
		}
		relative, err := url.Parse(next)
		if err != nil || strings.Contains(next, "\\") {
			return errors.New("API returned an invalid pagination link")
		}
		if _, err := s.Client.URL(next); err != nil {
			// Gitea builds links from its ROOT_URL, which can name the instance differently than the
			// configured host. Take the page from the link's query and keep the request on this instance.
			if !relative.IsAbs() || apiTail(relative.Path) == "" || apiTail(relative.Path) != apiTail(current.Path) {
				return fmt.Errorf("%w (next page link points to %s, the request went to %s)", err, relative.Host, current.Host)
			}
			same := *current
			same.RawQuery = relative.RawQuery
			relative = &same
		}
		endpoint = current.ResolveReference(relative).String()
	}
}

// emptyList turns the null body of response, a page of a list counted as empty, into [], so that a
// complete empty list reads as one. Gitea answers some empty lists, such as the timeline of an issue
// without events, with null. Any other body is left as it is.
func emptyList(response *http.Response) error {
	// A null with some whitespace around it is all there is to read.
	head, err := io.ReadAll(io.LimitReader(response.Body, 64))
	if err != nil {
		return err
	}
	if string(bytes.TrimSpace(head)) == "null" {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(strings.NewReader("[]"), response.Body), response.Body}
		response.ContentLength = -1
		response.Header.Del("Content-Length")
		return nil
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), response.Body), response.Body}
	return nil
}

// maxCountedPage bounds the part of a page Visit keeps to count its items; a larger page is not
// one of Gitea's list pages.
const maxCountedPage = 8 << 20

// capped keeps what is written to it up to limit bytes and drops the rest.
type capped struct {
	data     []byte
	limit    int
	overflow bool
}

func (c *capped) Write(p []byte) (int, error) {
	if len(c.data)+len(p) > c.limit {
		c.overflow = true
	} else {
		c.data = append(c.data, p...)
	}
	return len(p), nil
}

// unlinkedPages tracks the pages of a list that Gitea sends no Link header for.
type unlinkedPages struct {
	// listed counts the items of the pages read so far.
	listed int
	// last is the hash of the previous page.
	last [sha256.Size]byte
}

// past reads the body of response, a page that unlinkedNext asked for, into body, up to its limit,
// and reports whether the page lies past the end of the list: it is empty, null, holds no items
// or repeats the page before. Otherwise the body of response reads the page again, in full.
func (p *unlinkedPages) past(response *http.Response, body *capped) (bool, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(body.limit)+1))
	if err != nil {
		return false, err
	}
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), response.Body), response.Body}
	if len(data) > body.limit {
		body.overflow = true
		return false, nil
	}
	body.data = data
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" || sha256.Sum256(data) == p.last {
		return true, nil
	}
	items, _, ok := countedItems(response.Header, data)
	return ok && len(items) == 0, nil
}

// unlinkedNext returns the page after current of a list Gitea counts but sends no Link header
// for, or "" when current is its last page. Gitea counts most such lists, as of labels and
// milestones, in X-Total-Count, and some, as of the artifacts of a run, in the total_count field
// of an object holding the page's items. The list continues while it has fewer items than
// counted. The timeline counts only the items of each page, so when its count is just this
// page's, it continues while its pages are full. Visit does not visit the page asked for when it
// turns out to lie past the end, such as the null past the last full page of a timeline or the
// repeat of a list that ignores the page asked for.
func (s API) unlinkedNext(ctx context.Context, current *url.URL, headers http.Header, body *capped, pages *unlinkedPages) string {
	if body.overflow {
		return ""
	}
	items, total, ok := countedItems(headers, body.data)
	if !ok || len(items) == 0 {
		return ""
	}
	pages.last = sha256.Sum256(body.data)
	pages.listed += len(items)
	query := current.Query()
	page, err := strconv.Atoi(cmp.Or(query.Get("page"), "1"))
	if err != nil || page < 1 {
		return ""
	}
	if pages.listed >= total {
		if len(items) != total || !strings.HasSuffix(strings.TrimRight(current.Path, "/"), "/timeline") {
			return ""
		}
		sizes := pageSizesOf(ctx, s.Client)
		size := sizes.standard
		if limit, err := strconv.Atoi(query.Get("limit")); err == nil && limit > 0 {
			size = limit
			if sizes.max > 0 {
				size = min(size, sizes.max)
			}
		}
		if size <= 0 || len(items) < size {
			return ""
		}
	}
	query.Set("page", strconv.Itoa(page+1))
	next := *current
	next.RawQuery = query.Encode()
	return next.String()
}

// countedItems returns the items of a list page and the count of the whole list: the page's JSON
// array, or the one array of a JSON object such as the topics of a topic search, counted in
// X-Total-Count, or else the one array of a JSON object counted in its total_count field. ok is
// false for any other page.
func countedItems(headers http.Header, data []byte) (items []json.RawMessage, total int, ok bool) {
	total, err := strconv.Atoi(headers.Get("X-Total-Count"))
	counted := err == nil
	if counted && json.Unmarshal(data, &items) == nil {
		return items, total, true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || (!counted && json.Unmarshal(object["total_count"], &total) != nil) {
		return nil, 0, false
	}
	found := false
	for key, value := range object {
		var list []json.RawMessage
		if key == "total_count" || json.Unmarshal(value, &list) != nil {
			continue
		}
		if found {
			return nil, 0, false
		}
		items, found = list, true
	}
	return items, total, found
}

// apiTail returns the part of a path from its /api/ segment on.
func apiTail(path string) string {
	if i := strings.Index(path, "/api/"); i >= 0 {
		return path[i:]
	}
	return ""
}
