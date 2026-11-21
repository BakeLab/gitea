// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
			requirePayload(t, `{"website":"","default_branch":"main","private":false,"template":false,"has_actions":false,"has_wiki":false,"default_merge_style":"squash","allow_manual_merge":true,"autodetect_manual_merge":false}`, r.Body)
			_, _ = w.Write([]byte(`{"name":"project","full_name":"alice/project","website":"","default_branch":"main"}`))
		case "GET /api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"name":"project","full_name":"alice/project","website":"","default_branch":"main"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "-R", "alice/project", "--website=", "--default-branch", "main", "--private=false", "--as-template=false", "--actions=false", "--enable-wiki=false", "--allow-manual-merge", "--autodetect-manual-merge=false", "--default-merge-style", "squash", "--json=name,website,default_branch"}))
	require.JSONEq(t, `{"name":"project","website":"","default_branch":"main"}`, out.String())
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/main", "PATCH /api/v1/repos/alice/project", "GET /api/v1/repos/alice/project"}, requests)
}

func TestRepositoryEditNamesWhatIsMissing(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "--default-branch", "nope"})
	require.EqualError(t, err, `branch "nope" not found in alice/project`)
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/branches/nope", "GET /api/v1/repos/alice/project"}, requests)
	for _, flag := range []string{"--add-topic", "--default-branch"} {
		err = Execute(context.Background(), deps, []string{"repo", "edit", "alice/missing", flag, "x"})
		require.EqualError(t, err, "repository alice/missing not found", flag)
	}
}

func TestRepositoryEditReportsTheChangedSettings(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "PATCH /api/v1/repos/alice/project":
			requirePayload(t, `{"description":"CLI","private":true}`, r.Body)
		case "GET /api/v1/repos/alice/project/topics":
			_, _ = w.Write([]byte(`{"topics":["old","keep"]}`))
			return
		case "PUT /api/v1/repos/alice/project/topics":
			requirePayload(t, `{"topics":["keep","cli","go"]}`, r.Body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "CLI", "--visibility", "private", "--add-topic", "cli,go", "--remove-topic", "old"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/topics", "PATCH /api/v1/repos/alice/project", "PUT /api/v1/repos/alice/project/topics"}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Edited repository alice/project (add-topic, description, remove-topic, visibility)\n", diagnostics.String())
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

func TestRepositoryEditChecksTopicsBeforeChangingAnything(t *testing.T) {
	requireUsageBeforeRequests(t, `invalid topic "Bad Topic"`, "repo", "edit", "alice/project", "-d", "new", "--add-topic", "Bad Topic")
}

func TestRepositoryEditIgnoresRemovingAMissingTopic(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"full_name":"alice/project","topics":["other"]}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "new", "--remove-topic", "legacy"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/topics", "PATCH /api/v1/repos/alice/project"}, requests)
}

func TestRepositoryEditRejectsTooManyTopicsBeforeChangingAnything(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		var topics []string
		for i := range 25 {
			topics = append(topics, fmt.Sprintf("t%d", i))
		}
		data, _ := json.Marshal(map[string]any{"topics": topics})
		_, _ = w.Write(data)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "new", "--add-topic", "extra"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "at most 25 topics")
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/topics"}, requests)
}

func TestRepositoryEditReportsSettingsAppliedWhenTheTopicsFail(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Topic names are invalid"}`))
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"repo", "edit", "alice/project", "-d", "new", "--add-topic", "cli"})
	require.ErrorContains(t, err, "repository settings were changed, but setting the topics failed")
}
