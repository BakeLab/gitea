// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPullDiffStripsTerminalControlsOnlyOnATerminal(t *testing.T) {
	const diff = "+hello \x1b]0;PWNED\x07 \x1b[31mred\x1b[0m\r\n-old\tline\n"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/2.diff", r.URL.Path)
		_, _ = w.Write([]byte(diff))
	}))
	defer server.Close()
	for _, tc := range []struct {
		tty   bool
		args  []string
		color string
		want  string
	}{
		{false, nil, "", diff},
		{true, nil, "1", "+hello ]0;PWNED [31mred[0m\n-old\tline\n"},
		{true, nil, "", "\x1b[32m+hello ]0;PWNED [31mred[0m\x1b[0m\n\x1b[31m-old\tline\x1b[0m\n"},
		{false, []string{"--color", "always"}, "", "\x1b[32m+hello ]0;PWNED [31mred[0m\x1b[0m\n\x1b[31m-old\tline\x1b[0m\n"},
	} {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": tc.color})
		deps.Streams.OutTTY = tc.tty
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "diff", "2", "-R", "alice/project"}, tc.args...)))
		require.Equal(t, tc.want, out.String(), "%+v", tc)
	}
}

func TestPullDiffColorsHeadersAndHunks(t *testing.T) {
	text, err := diffText(strings.NewReader("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n context\n"), true)
	require.NoError(t, err)
	require.Equal(t, "\x1b[1mdiff --git a/x b/x\x1b[0m\n\x1b[1m--- a/x\x1b[0m\n\x1b[1m+++ b/x\x1b[0m\n\x1b[36m@@ -1 +1 @@\x1b[0m\n context\n", text)
}

func TestPullDiffNameOnly(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls/2/files", r.URL.Path)
		writeFirstPage(w, r, `[{"filename":"one.txt"},{"filename":"docs/two.md"}]`)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "diff", "2", "-R", "alice/project", "--name-only"}))
	require.Equal(t, "one.txt\ndocs/two.md\n", out.String())
}

func TestPullCloseCommentsFirstAndDeletesTheBranchOnlyWhenAsked(t *testing.T) {
	for _, tc := range []struct {
		flags   []string
		deleted bool
	}{
		{nil, false},
		{[]string{"--delete-branch"}, true},
	} {
		var requests []string
		server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.Method+" "+r.URL.Path)
			switch {
			case r.Method == http.MethodGet:
				_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
			case r.Method == http.MethodPatch:
				_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","base":{"ref":"main"},"head":{"ref":"feature","sha":"abc123","repo":{"full_name":"alice/project","permissions":{"push":true}}}}`))
			case r.Method == http.MethodPost:
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":1}`))
			default:
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		deps, _, _ := serverDeps(t, server.URL)
		deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "main"}}
		require.NoError(t, Execute(context.Background(), deps, append([]string{"pr", "close", "3", "-R", "alice/project", "-c", "Superseded"}, tc.flags...)))
		server.Close()
		comment := slices.Index(requests, "POST /api/v1/repos/alice/project/issues/3/comments")
		edit := slices.Index(requests, "PATCH /api/v1/repos/alice/project/pulls/3")
		require.True(t, comment >= 0 && comment < edit, requests)
		require.Equal(t, tc.deleted, slices.Contains(requests, "DELETE /api/v1/repos/alice/project/branches/feature"), requests)
	}
}

func TestPullCloseAndReopenRefuseMergedPullRequests(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"closed","merged":true}`))
	}))
	defer server.Close()
	for verb, want := range map[string]string{"close": "cannot be closed", "reopen": "cannot be reopened"} {
		deps, _, _ := serverDeps(t, server.URL)
		require.ErrorContains(t, Execute(context.Background(), deps, []string{"pr", verb, "3", "-R", "alice/project"}), want)
	}
}

func TestPullReopenWarnsWhenAlreadyOpen(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		_, _ = w.Write([]byte(`{"number":3,"title":"Add login","state":"open"}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "reopen", "3", "-R", "alice/project"}))
	require.Contains(t, diagnostics.String(), "is already open")
}
