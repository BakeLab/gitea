// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
