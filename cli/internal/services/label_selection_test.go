// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLabelSelectionReadsCappedPagesAndPreservesOrder(t *testing.T) {
	reads := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		if r.URL.Path == "/api/v1/orgs/alice" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		reads++
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"id":2,"name":"needs review"}]`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	service := Labels{Client: client, Repository: "alice/project"}
	ids, err := service.ResolveIssueNames(context.Background(), []string{"needs review", "bug", "bug"})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1}, ids)
	require.Equal(t, 3, reads)
	_, err = service.ResolveIssueNames(context.Background(), []string{"missing"})
	require.ErrorContains(t, err, "not found")
	before := reads
	_, err = service.ResolveIssueNames(context.Background(), []string{" "})
	require.ErrorContains(t, err, "cannot be empty")
	require.Equal(t, before, reads)
}

func TestIssueLabelsCombineScopesWithoutSilentlyChoosingDuplicates(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		if r.URL.Path == "/api/v1/orgs/company" {
			_, _ = w.Write([]byte(`{"id":9}`))
			return
		}
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/repos/company/project/labels":
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
		case "/api/v1/orgs/company/labels":
			_, _ = w.Write([]byte(`[{"id":2,"name":"bug"},{"id":3,"name":"shared"}]`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Labels{Client: client, Repository: "company/project"}
	_, err := service.ResolveIssueNames(context.Background(), []string{"bug"})
	require.EqualError(t, err, `label "bug" is ambiguous in company/project (IDs 1, 2); prefix the name with repo: or org: to choose one`)
	ids, err := service.ResolveIssueNames(context.Background(), []string{"org:bug", "repo:bug", "shared", "shared"})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 1, 3}, ids)
	// Names without an exact match fall back to the case-insensitive one, as label edit does.
	ids, err = service.ResolveIssueNames(context.Background(), []string{"Shared", "org:BUG"})
	require.NoError(t, err)
	require.Equal(t, []int64{3, 2}, ids)
}

func TestScopedLabelNamesDoNotCollideWithLiteralPrefixes(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/orgs/alice" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"bug"},{"id":2,"name":"repo:bug"},{"id":3,"name":"org:bug"}]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	service := Labels{Client: client, Repository: "alice/project"}
	ids, err := service.ResolveIssueNames(context.Background(), []string{"repo:bug", "repo:repo:bug", "repo:org:bug", "repo:bug"})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, ids)
	_, err = service.ResolveIssueNames(context.Background(), []string{"org:bug"})
	require.ErrorContains(t, err, "not found")
}

func TestLabelCreateUniqueRefusesOrUpdatesAFoldedName(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{"id":9,"name":"bug"}`))
	}))
	service := Labels{Client: client, Repository: "alice/project"}
	existing := []Label{{ID: 1, Name: "Bug", Archived: true}, {ID: 2, Name: "docs"}}
	_, _, err := service.CreateUnique(context.Background(), existing, LabelOptions{Name: new("bug")}, false)
	require.EqualError(t, err, `label "Bug" already exists in alice/project`)
	require.Empty(t, requests)

	_, updated, err := service.CreateUnique(context.Background(), existing, LabelOptions{Name: new("bug"), Color: new("ff0000")}, true)
	require.NoError(t, err)
	require.True(t, updated)
	_, updated, err = service.CreateUnique(context.Background(), existing, LabelOptions{Name: new("feature"), Color: new("00ff00")}, true)
	require.NoError(t, err)
	require.False(t, updated)
	require.Equal(t, []string{
		`PATCH /api/v1/repos/alice/project/labels/1 {"name":null,"color":"ff0000","description":null,"exclusive":null,"is_archived":true}`,
		`POST /api/v1/repos/alice/project/labels {"name":"feature","color":"00ff00","description":"","exclusive":false,"is_archived":false}`,
	}, requests)
}

func TestLabelEditUniqueRefusesAnotherLabelsName(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{"id":2,"name":"Docs"}`))
	}))
	service := Labels{Client: client, Organization: "company"}
	existing := []Label{{ID: 1, Name: "Bug"}, {ID: 2, Name: "docs", Archived: true}}
	_, err := service.EditUnique(context.Background(), existing, existing[1], LabelOptions{Name: new(" BUG ")})
	require.EqualError(t, err, `label "Bug" already exists in company`)
	require.Empty(t, requests)

	_, err = service.EditUnique(context.Background(), existing, existing[1], LabelOptions{Name: new(" Docs ")})
	require.NoError(t, err)
	require.Equal(t, []string{`PATCH /api/v1/orgs/company/labels/2 {"name":"Docs","color":null,"description":null,"is_archived":true}`}, requests)
}

func TestLabelRequireFilterNamesMatchesExactRepositoryNames(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		writeFirstPage(w, r, `[{"id":1,"name":"bug"},{"id":2,"name":"7"}]`)
	}))
	service := Labels{Client: client, Repository: "alice/project"}
	require.NoError(t, service.RequireFilterNames(context.Background(), []string{"bug", "7"}))
	require.EqualError(t, service.RequireFilterNames(context.Background(), []string{"bug", "Bug"}),
		`label "Bug" does not exist in alice/project; issue filters match repository labels only`)

	found, err := service.Find(context.Background(), []string{"7", "BUG"}, true)
	require.NoError(t, err)
	require.Equal(t, []Label{{ID: 2, Name: "7"}, {ID: 1, Name: "bug"}}, found)
	found, err = service.Find(context.Background(), []string{"2"}, false)
	require.NoError(t, err)
	require.Equal(t, []Label{{ID: 2, Name: "7"}}, found)
	_, err = service.Find(context.Background(), []string{"bug", "2"}, true)
	require.EqualError(t, err, `label "2" not found in alice/project`)
}
