// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

// upstreamCheckout discovers the repository from the upstream remote, as in a clone of a fork.
func upstreamCheckout(server *httptest.Server) *fakeGit {
	return &fakeGit{results: map[string]string{
		"symbolic-ref --short HEAD":       "main",
		"config --get branch.main.remote": "upstream",
		"remote get-url upstream":         server.URL + "/alice/project.git",
	}}
}

func TestRepoDeleteAndArchiveRequireExplicitRepositoryWithYes(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, verb := range []string{"delete", "archive"} {
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = upstreamCheckout(server)
		err := Execute(context.Background(), deps, []string{"repo", verb, "--yes"})
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, "--yes requires an explicit repository, not one inferred from git remote upstream: gt repo "+verb+" alice/project --yes")
	}
	require.Empty(t, requests)
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = upstreamCheckout(server)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "delete", "alice/project", "--yes"}))
	require.Equal(t, []string{"DELETE /api/v1/repos/alice/project"}, requests)
}

func TestRepoDeleteAsksToTypeTheRepositoryName(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, test := range []struct {
		answer  string
		deleted bool
	}{{"alice/other", false}, {"", false}, {"alice/project", true}} {
		requests = nil
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = upstreamCheckout(server)
		prompt := &scriptedPrompter{inputs: map[string]string{"Delete alice/project (from git remote upstream)? This cannot be undone. Type alice/project to confirm:": test.answer}}
		deps.Prompter = prompt
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		err := Execute(context.Background(), deps, []string{"repo", "delete"})
		require.Len(t, prompt.asked, 1)
		require.Empty(t, prompt.confirmations)
		if !test.deleted {
			require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
			require.Empty(t, requests)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []string{"DELETE /api/v1/repos/alice/project"}, requests)
	}
}
