// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
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
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/nope", "GET /api/v1/repos/alice/project"}, requests)
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

func TestRepositoryEditArchivesAfterTheOtherChanges(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		request := r.Method + " " + r.URL.Path
		if strings.Contains(string(data), `"archived":true`) {
			request += " archived"
		}
		requests = append(requests, request)
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "CLI", "--archived", "--add-topic", "cli", "--yes"}))
	require.Equal(t, []string{
		"PATCH /api/v1/repos/alice/project",
		"PUT /api/v1/repos/alice/project/topics/cli",
		"PATCH /api/v1/repos/alice/project archived",
	}, requests)
}

func TestRepositoryEditSetsTheDefaultBranchOfAnEmptyRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","empty":true}`))
		case "PATCH /api/v1/repos/alice/project":
			requirePayload(t, `{"default_branch":"develop"}`, r.Body)
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "--default-branch", "develop"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/develop", "GET /api/v1/repos/alice/project", "PATCH /api/v1/repos/alice/project"}, requests)
}

func TestRepositoryEditArchiveRequiresAnExplicitRepositoryWithYes(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = upstreamCheckout(server)
	err := Execute(context.Background(), deps, []string{"repo", "edit", "--archived", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "--yes requires an explicit repository, not one inferred from git remote upstream")
}

func TestRepositoryEditChecksTopicsBeforeChangingAnything(t *testing.T) {
	requireUsageBeforeRequests(t, `invalid topic "Bad Topic"`, "repo", "edit", "alice/project", "-d", "new", "--add-topic", "Bad Topic")
}

func TestRepositoryEditIgnoresRemovingAMissingTopic(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "new", "--remove-topic", "legacy"}))
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project", "DELETE /api/v1/repos/alice/project/topics/legacy"}, requests)
}
