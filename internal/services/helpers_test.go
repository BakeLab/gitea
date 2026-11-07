// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/cli/internal/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient serves handler for the duration of the test and returns a client for it.
// Requests probing the server version or API schema fail the test: gt targets one Gitea
// release and must never gate operations on either.
func newTestClient(t *testing.T, handler http.Handler) *transport.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/api/v1/version") || strings.HasSuffix(r.URL.Path, "/swagger.v1.json") {
			t.Errorf("unexpected capability probe %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return transport.New(server.URL, "", false)
}

// requestJSON decodes the JSON object a handler received.
func requestJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	return payload
}

func TestNameOrIDPrefersIDsThenExactThenFoldedNames(t *testing.T) {
	type label struct {
		id   int64
		name string
	}
	labels := []label{{11, "bug"}, {12, "Bug"}, {13, "7"}, {7, "seven"}, {20, "kind/feature"}, {21, "dup"}, {22, "dup"}}
	lookup := NameOrID[label]{Kind: "label", Scope: "alice/project", ID: func(l label) int64 { return l.id }, Name: func(l label) string { return l.name }}
	for _, tc := range []struct {
		raw    string
		byName bool
		want   int64
		err    string
	}{
		{raw: "7", want: 7},
		{raw: "7", byName: true, want: 13},
		{raw: "13", want: 13},
		{raw: "bug", want: 11},
		{raw: "BUG", err: `label "BUG" is ambiguous in alice/project (IDs 11, 12); pass an ID`},
		{raw: "KIND/FEATURE", want: 20},
		{raw: " kind/feature ", want: 20},
		{raw: "dup", err: `label "dup" is ambiguous in alice/project (IDs 21, 22); pass an ID`},
		{raw: "nope", err: `label "nope" not found in alice/project`},
		{raw: "99", err: "label 99 not found in alice/project"},
		{raw: "99", byName: true, err: `label "99" not found in alice/project`},
	} {
		lookup.ByName = tc.byName
		got, err := lookup.Find(labels, tc.raw)
		if tc.err != "" {
			require.EqualError(t, err, tc.err, tc.raw)
			continue
		}
		require.NoError(t, err, tc.raw)
		require.Equal(t, tc.want, got.id, tc.raw)
	}
}

func TestNameOrIDExactMatchesNamesOnlyInTheirOwnCase(t *testing.T) {
	type asset struct {
		id   int64
		name string
	}
	assets := []asset{{7, "GT-LINUX"}, {8, "notes.txt"}}
	lookup := NameOrID[asset]{Kind: "asset", Scope: "release v1", Exact: true, ID: func(a asset) int64 { return a.id }, Name: func(a asset) string { return a.name }}
	got, err := lookup.Find(assets, "GT-LINUX")
	require.NoError(t, err)
	require.Equal(t, int64(7), got.id)
	got, err = lookup.Find(assets, "8")
	require.NoError(t, err)
	require.Equal(t, int64(8), got.id)
	_, err = lookup.Find(assets, "gt-linux")
	require.EqualError(t, err, `asset "gt-linux" not found in release v1`)
}

// writeFirstPage answers the first page of a listing with page and every later page with [].
func writeFirstPage(w http.ResponseWriter, r *http.Request, page string) {
	if r.URL.Query().Get("page") != "1" {
		page = `[]`
	}
	_, _ = w.Write([]byte(page))
}

func TestPageOfCountsTheWholeCollection(t *testing.T) {
	for _, tc := range []struct {
		page int
		want []int
		more bool
	}{{1, []int{1, 2}, true}, {2, []int{3, 4}, false}, {3, []int{}, false}} {
		result, err := PageOf([]int{1, 2, 3, 4}, 2, tc.page)
		require.NoError(t, err)
		require.Equal(t, Page[int]{Items: tc.want, More: tc.more, Total: 4, Counted: true}, result)
	}
}

// waitTogether fails unless every request wg counts arrives while the others wait.
func waitTogether(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the requests did not run concurrently")
	}
}

func TestListEveryReadsCountedPagesTogether(t *testing.T) {
	const pages = 4
	var arrived sync.WaitGroup
	arrived.Add(pages - 1)
	got, err := listEvery(2, func(limit, page int) (Page[int], error) {
		if page > 1 {
			arrived.Done()
			waitTogether(t, &arrived)
		}
		return Page[int]{Items: []int{page*2 - 1, page * 2}, More: page < pages, Total: pages * 2, Counted: true}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8}, got)
}

func TestListEveryOutlivesAWrongCount(t *testing.T) {
	window := func(items []int, more bool, total int) Page[int] {
		return Page[int]{Items: items, More: more, Total: total, Counted: true}
	}
	t.Run("too few", func(t *testing.T) {
		got, err := listEvery(2, func(_, page int) (Page[int], error) {
			switch page {
			case 1:
				return window([]int{1, 2}, true, 3), nil
			case 2:
				return window([]int{3, 4}, true, 3), nil
			case 3:
				return window([]int{5}, false, 3), nil
			}
			return window(nil, false, 3), nil
		})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2, 3, 4, 5}, got)
	})
	t.Run("too many", func(t *testing.T) {
		got, err := listEvery(2, func(_, page int) (Page[int], error) {
			if page == 1 {
				return window([]int{1, 2}, true, 8), nil
			}
			if page == 3 {
				return Page[int]{}, errors.New("past the empty window")
			}
			return window(nil, false, 8), nil
		})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2}, got)
	})
}

func TestListEveryFailsWithTheFirstFailedPage(t *testing.T) {
	_, err := listEvery(2, func(_, page int) (Page[int], error) {
		if page > 1 {
			return Page[int]{}, fmt.Errorf("page %d", page)
		}
		return Page[int]{Items: []int{1, 2}, More: true, Total: 8, Counted: true}, nil
	})
	require.EqualError(t, err, "page 2")
}
