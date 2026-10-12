// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
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
	for tty, want := range map[bool]string{false: diff, true: "+hello ]0;PWNED [31mred[0m\n-old\tline\n"} {
		deps, out, _ := serverDeps(t, server.URL)
		deps.Streams.OutTTY = tty
		require.NoError(t, Execute(context.Background(), deps, []string{"pr", "diff", "2", "-R", "alice/project"}))
		require.Equal(t, want, out.String(), "tty=%t", tty)
	}
}
