// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestPullViewResolvesCurrentBranch(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/pulls", r.URL.Path)
		require.Equal(t, "open", r.URL.Query().Get("state"))
		if r.URL.Query().Get("page") != "1" {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		_, _ = fmt.Fprint(w, `[{"number":9,"title":"Feature","html_url":"https://example.org/pr/9","state":"open","head":{"ref":"feature","sha":"abc"}}]`)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --short HEAD": "feature"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "-R", "alice/project", "--json=number,title"}))
	require.JSONEq(t, `{"number":9,"title":"Feature"}`, out.String())
	deps.Git = &fakeGit{results: map[string]string{}}
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"pr", "view", "-R", "alice/project"})))
}
