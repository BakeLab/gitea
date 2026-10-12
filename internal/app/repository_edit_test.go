// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryEditPreservesExplicitClearsAndFalse(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case "PATCH /api/v1/repos/alice/project":
			requirePayload(t, `{"name":"renamed","website":"","default_branch":"main","archived":false,"private":false,"template":false,"has_actions":false,"has_wiki":false,"default_merge_style":"squash"}`, r.Body)
			_, _ = w.Write([]byte(`{"name":"renamed","full_name":"alice/renamed","website":"","default_branch":"main"}`))
		case "GET /api/v1/repos/alice/renamed":
			_, _ = w.Write([]byte(`{"name":"renamed","full_name":"alice/renamed","website":"","default_branch":"main"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "-R", "alice/project", "--name", "renamed", "--website=", "--default-branch", "main", "--archived=false", "--private=false", "--as-template=false", "--actions=false", "--enable-wiki=false", "--default-merge-style", "squash", "--json=name,website,default_branch"}))
	require.JSONEq(t, `{"name":"renamed","website":"","default_branch":"main"}`, out.String())
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/main", "PATCH /api/v1/repos/alice/project", "GET /api/v1/repos/alice/renamed"}, requests)
}

func TestRepositoryEditRejectsAMissingDefaultBranch(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "--default-branch", "nope"})
	require.EqualError(t, err, `branch "nope" not found in alice/project`)
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/nope"}, requests)
}

func TestRepositoryEditReportsTheChangedSettings(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			requirePayload(t, `{"description":"CLI","private":true}`, r.Body)
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "CLI", "--visibility", "private", "--add-topic", "cli,go", "--remove-topic", "old"}))
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project", "PUT /api/v1/repos/alice/project/topics/cli", "PUT /api/v1/repos/alice/project/topics/go", "DELETE /api/v1/repos/alice/project/topics/old"}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Edited repository alice/project (add-topic, description, remove-topic, visibility)\n", diagnostics.String())
}
