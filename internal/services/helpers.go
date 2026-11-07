// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// maxListPages bounds exhaustive listings so a misbehaving server cannot loop forever.
const maxListPages = 100

// pageOf applies CLI pagination to a collection the API returns unpaginated.
func pageOf[T any](items []T, limit, page int) ([]T, error) {
	if limit < 1 || page < 1 {
		return nil, errors.New("limit and page must be positive")
	}
	if page-1 > len(items)/limit {
		return []T{}, nil
	}
	start := (page - 1) * limit
	return items[start:min(start+limit, len(items))], nil
}

// PageOf returns one CLI page of a complete collection, counting the whole collection.
func PageOf[T any](items []T, limit, page int) (Page[T], error) {
	window, err := pageOf(items, limit, page)
	return Page[T]{Items: window, More: page*limit < len(items), Total: len(items), Counted: true}, err
}

// loadTogether runs the loaders concurrently and returns their errors joined.
func loadTogether(loaders ...func() error) error {
	return concurrently(len(loaders), func(i int) error { return loaders[i]() })
}

// listEvery collects every item of a collection from fetch, which reads a window of limit items
// with instancePage. It stops at an empty window, or at the window that reaches the size the server
// counts, without asking for an empty page after it. A server that counts nothing, or counts
// fewer items than it lists, is read until an empty window.
func listEvery[T any](limit int, fetch func(limit, page int) (Page[T], error)) ([]T, error) {
	result := []T{}
	for page := 1; page <= maxListPages; page++ {
		window, err := fetch(limit, page)
		if err != nil {
			return nil, err
		}
		result = append(result, window.Items...)
		if len(window.Items) == 0 || window.Counted && !window.More && len(result) == window.Total {
			return result, nil
		}
	}
	return nil, fmt.Errorf("listing exceeded %d pages", maxListPages)
}

// callAll is listEvery over an SDK list call.
func callAll[T any](ctx context.Context, client *transport.Client, limit int, list func(context.Context, *gitea.Client, gitea.ListOptions) ([]T, *gitea.Response, error)) ([]T, error) {
	return listEvery(limit, func(limit, page int) (Page[T], error) { return callPage(ctx, client, limit, page, list) })
}

// filterPageSize is the page size filteredPage reads, Gitea's default maximum.
const filterPageSize = 50

// filteredPage applies a filter without a server equivalent: it reads pages of filterPageSize
// from fetch only until one item past the requested page is kept, and counts the matches when
// it reads them all.
func filteredPage[T any](limit, page int, fetch func(limit, page int) (Page[T], error), keep func(T) bool) (Page[T], error) {
	if limit <= 0 || page <= 0 {
		return Page[T]{}, errors.New("limit and page must be positive")
	}
	matching, wanted := []T{}, page*limit+1
	for serverPage := 1; ; serverPage++ {
		if serverPage > maxListPages {
			return Page[T]{}, fmt.Errorf("listing exceeded %d pages", maxListPages)
		}
		window, err := fetch(filterPageSize, serverPage)
		if err != nil {
			return Page[T]{}, err
		}
		for _, item := range window.Items {
			if keep(item) {
				matching = append(matching, item)
			}
		}
		if len(matching) >= wanted {
			items, err := pageOf(matching, limit, page)
			return Page[T]{Items: items, More: true}, err
		}
		if len(window.Items) == 0 || (!window.More && len(window.Items) < filterPageSize) {
			break
		}
	}
	items, err := pageOf(matching, limit, page)
	if err != nil {
		return Page[T]{}, err
	}
	return Page[T]{Items: items, More: page*limit < len(matching), Total: len(matching), Counted: true}, nil
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

// instancePage reads the window of limit items at page from fetch, even when client's instance
// caps its page size below limit, and reports what the server counted. The instance's largest
// page tells a page it capped from the last one.
func instancePage[T any](ctx context.Context, client *transport.Client, limit, page int, fetch func(limit, page int) ([]T, http.Header, error)) (Page[T], error) {
	return cappedPage(limit, page, func() int { return maxPageSize(ctx, client) }, fetch)
}

// pageSizes holds the page sizes each client's instance reports, as instancePageSizes.
var pageSizes sync.Map

// instancePageSizes are the largest page an instance answers, its MAX_RESPONSE_ITEMS, and the
// page it answers without a limit, its DEFAULT_PAGING_NUM; each is 0 when it does not say.
type instancePageSizes struct{ max, standard int }

// pageSizesOf returns the page sizes of client's instance. It asks once per client.
func pageSizesOf(ctx context.Context, client *transport.Client) instancePageSizes {
	if cached, ok := pageSizes.Load(client); ok {
		if sizes, ok := cached.(instancePageSizes); ok {
			return sizes
		}
	}
	settings, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (*gitea.GlobalAPISettings, *gitea.Response, error) {
		return c.Settings.GetGlobalAPISettings(ctx)
	})
	if ctx.Err() != nil {
		return instancePageSizes{}
	}
	sizes := instancePageSizes{}
	if err == nil && settings != nil {
		sizes = instancePageSizes{max: settings.MaxResponseItems, standard: settings.DefaultPagingNum}
	}
	pageSizes.Store(client, sizes)
	return sizes
}

// maxPageSize returns the largest page client's instance answers, its MAX_RESPONSE_ITEMS, or 0
// when it does not say. It asks once per client.
func maxPageSize(ctx context.Context, client *transport.Client) int {
	return pageSizesOf(ctx, client).max
}

// cappedPage is instancePage; maxSize returns the largest page the server answers, or 0 when it
// is unknown.
func cappedPage[T any](limit, page int, maxSize func() int, fetch func(limit, page int) ([]T, http.Header, error)) (Page[T], error) {
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
	// means the server capped the page size. Past page 1 the size of a capped page can account
	// for the remainder too, so a page as large as the largest the server answers is a capped
	// one, unless the server sent links without a next one: its links count pages of the size it
	// answers, so a capped page past page 1 always has a next link. Pages of the returned size
	// are never capped again.
	linked := len(headers.Values("Link")) > 0
	if !next && (!counted || total == (page-1)*limit+size && (page == 1 || linked || size != maxSize())) {
		return Page[T]{Items: items, Total: total, Counted: counted}, nil
	}
	// A page shorter than the largest one the server answers is a filtered one: Gitea counts and
	// pages over repositories the viewer cannot see. Pages then keep the server's positions, read
	// in pages that start where the window does. A page of filterPageSize is taken as capped to
	// spare the lookup on instances with Gitea's default maximum.
	if size != filterPageSize {
		if largest := maxSize(); largest > 0 && size < min(limit, largest) {
			if limit <= largest {
				return Page[T]{Items: items, More: next || (counted && total > page*limit), Total: total, Counted: counted}, nil
			}
			return positionedPage(limit, page, largest, items, headers, fetch)
		}
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
		if serverPage*size >= offset+limit || lastServerPage(headers, serverPage*size, len(batch), size) {
			break
		}
	}
	next, total, counted = pageHints(headers)
	more := len(result) > limit || next || (counted && total > offset+limit)
	return Page[T]{Items: result[:min(len(result), limit)], More: more, Total: total, Counted: counted}, nil
}

// positionedPage reads the window of limit items at page from server pages of at most largest
// that start and end inside it, so no server page straddles two windows; first and its headers
// are the server's page at page in pages of largest.
func positionedPage[T any](limit, page, largest int, first []T, headers http.Header, fetch func(limit, page int) ([]T, http.Header, error)) (Page[T], error) {
	offset, end := (page-1)*limit, page*limit
	result := []T{}
	for at := offset; at < end; {
		step := min(largest, end-at)
		for at%step != 0 {
			step--
		}
		batch := first
		if step != largest || at/step+1 != page {
			var err error
			if batch, headers, err = fetch(step, at/step+1); err != nil {
				return Page[T]{}, err
			}
		}
		result = append(result, batch...)
		if at += step; lastServerPage(headers, at, len(batch), step) {
			break
		}
	}
	next, total, counted := pageHints(headers)
	return Page[T]{Items: result, More: next || (counted && total > end), Total: total, Counted: counted}, nil
}

// lastServerPage reports whether a server page of size that ends at position end and held got
// items is the last one: a short page is, unless the server links or counts more.
func lastServerPage(headers http.Header, end, got, size int) bool {
	next, total, counted := pageHints(headers)
	return got < size && !next && (!counted || total <= end)
}

// callPage is instancePage over an SDK list call.
func callPage[T any](ctx context.Context, client *transport.Client, limit, page int, list func(context.Context, *gitea.Client, gitea.ListOptions) ([]T, *gitea.Response, error)) (Page[T], error) {
	return instancePage(ctx, client, limit, page, func(limit, page int) ([]T, http.Header, error) {
		items, resp, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) ([]T, *gitea.Response, error) {
			return list(ctx, c, listOptions(limit, page))
		})
		return items, headerOf(resp), err
	})
}

// NameOrID addresses one kind of resource by name or ID: a numeric value is an ID first, then a
// name; any other value is an exact name, then a unique case-insensitive name unless Exact is set.
type NameOrID[T any] struct {
	// Kind and Scope name the resources in errors, such as "label" in "alice/project".
	Kind, Scope string
	ID          func(T) int64
	Name        func(T) string
	// ByName matches numeric values as names only, for --by-name.
	ByName bool
	// Exact matches names case-sensitively only, for resources such as release assets whose
	// names may differ only in case.
	Exact bool
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
		if fold && n.Exact {
			break
		}
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

// FindAll returns the items among items that raws address, each like Find.
func (n NameOrID[T]) FindAll(items []T, raws []string) ([]T, error) {
	result := make([]T, len(raws))
	for i, raw := range raws {
		var err error
		if result[i], err = n.Find(items, raw); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// CreateUnique runs create unless an item among existing has name, ignoring case, which names
// would then no longer address. With update it runs edit on that item instead and reports true;
// otherwise a *ConflictError refuses it.
func (n NameOrID[T]) CreateUnique(existing []T, name string, update bool, create func() (T, error), edit func(T) (T, error)) (T, bool, error) {
	byName := n
	byName.ByName = true
	current, err := byName.Find(existing, name)
	if errors.As(err, new(*NotFoundError)) {
		item, err := create()
		return item, false, err
	}
	var zero T
	if err != nil {
		return zero, false, err
	}
	if !update {
		return zero, false, n.exists(current)
	}
	item, err := edit(current)
	return item, true, err
}

// RefuseRename refuses to rename current to name when another item among existing has it,
// ignoring case, which CreateUnique refuses too: names would no longer address either item.
func (n NameOrID[T]) RefuseRename(existing []T, current T, name string) error {
	if name == n.Name(current) {
		return nil
	}
	for _, other := range existing {
		if n.ID(other) != n.ID(current) && strings.EqualFold(n.Name(other), name) {
			return n.exists(other)
		}
	}
	return nil
}

func (n NameOrID[T]) exists(item T) error {
	return &ConflictError{Reason: ReasonExists, Kind: n.Kind, Key: strconv.Quote(n.Name(item)), Scope: n.Scope}
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
