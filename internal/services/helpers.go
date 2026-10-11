// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
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

// listWindow returns items [(page-1)*limit, page*limit) even when the server caps its page size
// below limit (MAX_RESPONSE_ITEMS), and reports whether the collection continues past them.
func listWindow[T any](limit, page int, fetch func(limit, page int) ([]T, http.Header, error)) ([]T, bool, error) {
	items, headers, err := fetch(limit, page)
	if err != nil {
		return nil, false, err
	}
	next, total, counted := pageHints(headers)
	size := len(items)
	if size == 0 || size >= limit {
		return items, next || (counted && total > page*limit), nil
	}
	// A short final page has no next link and accounts for the whole remainder; anything else
	// means the server capped the page size. Pages of the returned size are never capped again.
	if !next && (!counted || total == (page-1)*limit+size) {
		return items, false, nil
	}
	offset := (page - 1) * limit
	result := make([]T, 0, limit+size)
	for serverPage := offset/size + 1; ; serverPage++ {
		batch := items
		if serverPage != page {
			if batch, headers, err = fetch(size, serverPage); err != nil {
				return nil, false, err
			}
		}
		result = append(result, batch[min(len(batch), max(0, offset-(serverPage-1)*size)):]...)
		if len(batch) < size || len(result) >= limit {
			break
		}
	}
	next, total, counted = pageHints(headers)
	more := len(result) > limit || next || (counted && total > offset+limit)
	return result[:min(len(result), limit)], more, nil
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
