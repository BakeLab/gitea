// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitea.dev/cli/internal/transport"
)

// maxListPages bounds exhaustive listings so a misbehaving server cannot loop forever.
const maxListPages = 100

// pageOf applies CLI pagination to a collection the API returns unpaginated.
func pageOf[T any](items []T, limit, page int) ([]T, error) {
	if limit < 1 || page < 1 {
		return nil, fmt.Errorf("limit and page must be positive")
	}
	if page-1 > len(items)/limit {
		return []T{}, nil
	}
	start := (page - 1) * limit
	return items[start:min(start+limit, len(items))], nil
}

// listAll collects pages until the server returns an empty one.
func listAll[T any](fetch func(page int) ([]T, error)) ([]T, error) {
	result := []T{}
	for page := 1; page <= maxListPages; page++ {
		items, err := fetch(page)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return result, nil
		}
		result = append(result, items...)
	}
	return nil, fmt.Errorf("listing exceeded %d pages", maxListPages)
}

// pageHints reads whether the server advertises a next page and the collection size.
func pageHints(headers http.Header) (next bool, total int, counted bool) {
	links, err := nextPageLinks(headers.Values("Link"))
	total, countErr := strconv.Atoi(headers.Get("X-Total-Count"))
	return err == nil && len(links) > 0, total, countErr == nil && total >= 0
}

// Page is one window of a collection and what the server reported about the rest of it.
type Page[T any] struct {
	Items []T
	// More reports whether the collection continues past Items.
	More bool
	// Total is the size of the whole collection when Counted, from X-Total-Count.
	Total   int
	Counted bool
}

// listWindow returns items [(page-1)*limit, page*limit) even when the server caps its page size
// below limit (MAX_RESPONSE_ITEMS), and reports whether the collection continues past them.
func listWindow[T any](limit, page int, fetch func(limit, page int) ([]T, http.Header, error)) ([]T, bool, error) {
	window, err := listPage(limit, page, fetch)
	return window.Items, window.More, err
}

// listPage is listWindow that also reports the collection size the server counted.
func listPage[T any](limit, page int, fetch func(limit, page int) ([]T, http.Header, error)) (Page[T], error) {
	items, headers, err := fetch(limit, page)
	if err != nil {
		return Page[T]{}, err
	}
	next, total, counted := pageHints(headers)
	size := len(items)
	if size == 0 || size >= limit {
		return Page[T]{Items: items, More: next || (counted && total > page*limit), Total: total, Counted: counted}, nil
	}
	// A short final page has no next link and accounts for the whole remainder; anything else
	// means the server capped the page size. Pages of the returned size are never capped again.
	if !next && (!counted || total == (page-1)*limit+size) {
		return Page[T]{Items: items, Total: total, Counted: counted}, nil
	}
	offset := (page - 1) * limit
	result := make([]T, 0, limit+size)
	for serverPage := offset/size + 1; ; serverPage++ {
		batch := items
		if serverPage != page {
			if batch, headers, err = fetch(size, serverPage); err != nil {
				return Page[T]{}, err
			}
		}
		result = append(result, batch[min(len(batch), max(0, offset-(serverPage-1)*size)):]...)
		if len(batch) < size || len(result) >= limit {
			break
		}
	}
	next, total, counted = pageHints(headers)
	more := len(result) > limit || next || (counted && total > offset+limit)
	return Page[T]{Items: result[:min(len(result), limit)], More: more, Total: total, Counted: counted}, nil
}

// rawPage is listPage over a raw JSON collection endpoint; it sets limit and page on query.
func rawPage[T any](ctx context.Context, client *transport.Client, endpoint string, query url.Values, limit, page int) (Page[T], error) {
	query = maps.Clone(query)
	if query == nil {
		query = url.Values{}
	}
	return listPage(limit, page, func(limit, page int) ([]T, http.Header, error) {
		query.Set("limit", strconv.Itoa(limit))
		query.Set("page", strconv.Itoa(page))
		var response []T
		headers, err := rawJSON(ctx, client, http.MethodGet, endpoint+"?"+query.Encode(), nil, &response)
		return response, headers, err
	})
}

// NameOrID addresses one kind of resource by name or ID: a numeric value is an ID first, then a
// name; any other value is an exact name, then a unique case-insensitive name.
type NameOrID[T any] struct {
	// Kind and Scope name the resources in errors, such as "label" in "alice/project".
	Kind, Scope string
	ID          func(T) int64
	Name        func(T) string
	// ByName matches numeric values as names only, for --by-name.
	ByName bool
}

// Find returns the item among items that raw addresses. It fails with a *NotFoundError when
// nothing matches and an *AmbiguousError listing the IDs when several names match.
func (n NameOrID[T]) Find(items []T, raw string) (T, error) {
	var zero T
	raw = strings.TrimSpace(raw)
	if id, err := strconv.ParseInt(raw, 10, 64); err == nil && !n.ByName {
		for _, item := range items {
			if n.ID(item) == id {
				return item, nil
			}
		}
	}
	for _, fold := range []bool{false, true} {
		var matches []T
		for _, item := range items {
			name := n.Name(item)
			if name == raw || (fold && strings.EqualFold(name, raw)) {
				matches = append(matches, item)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		default:
			ids := make([]int64, len(matches))
			for i, match := range matches {
				ids[i] = n.ID(match)
			}
			return zero, &AmbiguousError{Kind: n.Kind, Key: strconv.Quote(raw), Scope: n.Scope, IDs: ids}
		}
	}
	key := strconv.Quote(raw)
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil && !n.ByName {
		key = raw
	}
	return zero, &NotFoundError{Kind: n.Kind, Key: key, Scope: n.Scope}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
