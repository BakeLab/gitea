// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
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
		if next == "" {
			return nil
		}
		if page+1 >= options.MaxPages {
			return ErrMaxPages
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

// apiTail returns the part of a path from its /api/ segment on.
func apiTail(path string) string {
	if i := strings.Index(path, "/api/"); i >= 0 {
		return path[i:]
	}
	return ""
}
