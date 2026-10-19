// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestPullCleanRejectsOpenPullBeforeGitMutation(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"open","head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	runner := &fakeGit{}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input"})
	require.ErrorContains(t, err, "still open")
	require.Empty(t, runner.calls)
	require.Empty(t, out.String())
}

func TestPullCleanDeletesLocalAndRemoteBranches(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main","clone_url":"https://example.org/alice/project.git"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"closed","merged":true,"head":{"sha":"abc123","ref":"feature","label":"alice:feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","ssh_url":"ssh://git@example.org/alice/project.git","permissions":{"push":true}}}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	configureHost(t, deps, server.URL, settings.Host{User: "alice", Users: tokenAccounts(settings.StoreFile, "alice")})
	deleteRemote := credentialCall(t, deps, server.URL, "alice", "push", "--delete", "--", "origin", "feature")
	runner := &fakeGit{results: map[string]string{
		deleteRemote:                      "",
		"remote":                          "origin",
		"remote get-url origin":           "https://example.org/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"for-each-ref --format=%(objectname)%09%(refname:short)%09%(upstream:remotename) refs/heads/": "abc123\tpr/7\torigin/feature",
		"check-ref-format --branch pr/7":              "",
		"check-ref-format --branch main":              "",
		"status --porcelain=v1 --untracked-files=no":  "",
		"rev-parse --verify refs/heads/pr/7^{commit}": "abc123",
		"rev-parse --verify refs/heads/main^{commit}": "mainsha",
		"symbolic-ref --quiet HEAD":                   "pr/7",
		"switch --quiet -- main":                      "",
		"branch -D -- pr/7":                           "",
	}}
	deps.Git = runner
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--json"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--yes")
	assertNoMutation := func() {
		t.Helper()
		for _, call := range runner.calls {
			require.NotContains(t, call, "switch --")
			require.NotContains(t, call, "branch -D")
			require.NotContains(t, call, "push --delete")
		}
		require.Empty(t, out.String())
	}
	assertNoMutation()
	runner.calls = nil
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{}
	deps.Prompter = prompt
	err = Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
	require.Len(t, prompt.confirmations, 1)
	assertNoMutation()
	runner.calls = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input", "--json", "--yes"}))
	require.Contains(t, runner.calls, "branch -D -- pr/7")
	require.Contains(t, runner.calls, "switch --quiet -- main")
	require.Contains(t, runner.calls, deleteRemote)
	require.Contains(t, out.String(), `"local_branch":"pr/7"`)
	require.Contains(t, out.String(), `"remote_deleted":true`)
}

func TestPullCleanRequiresIgnoreSHAWhenHeadMissingLocally(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = fmt.Fprint(w, `{"full_name":"alice/project","default_branch":"main"}`)
		case "/api/v1/repos/alice/project/pulls/7":
			_, _ = fmt.Fprint(w, `{"number":7,"state":"closed","head":{"sha":"abc123","ref":"feature","repo":{"full_name":"alice/project","clone_url":"https://example.org/alice/project.git","permissions":{"push":false}}}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{
		"remote":                          "origin",
		"remote get-url origin":           "https://example.org/alice/project.git",
		"rev-parse --is-inside-work-tree": "true",
		"for-each-ref --format=%(objectname)%09%(refname:short)%09%(upstream:remotename) refs/heads/": "deadbeef\tfeature\torigin/feature",
		"rev-parse --verify refs/remotes/origin/feature^{commit}":                                     "deadbeef",
	}}
	err := Execute(context.Background(), deps, []string{"pr", "clean", "7", "--repo", "alice/project", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "--ignore-sha")
	require.Empty(t, out.String())
}
