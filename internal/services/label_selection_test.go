// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

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
