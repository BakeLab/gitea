// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
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
		{true, []string{"--color", "never"}, "", "+hello ]0;PWNED [31mred[0m\n-old\tline\n"},
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
