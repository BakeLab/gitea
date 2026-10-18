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
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/pulls/9":
			_, _ = fmt.Fprint(w, `{"number":9,"title":"Feature","state":"open","head":{"ref":"feature","sha":"abc"}}`)
		case r.URL.Query().Get("page") != "1":
			_, _ = fmt.Fprint(w, `[]`)
		case r.URL.Path == "/api/v1/repos/alice/project/pulls":
			require.Equal(t, "open", r.URL.Query().Get("state"))
			_, _ = fmt.Fprint(w, `[{"number":9,"title":"Feature","html_url":"https://example.org/pr/9","state":"open","head":{"ref":"feature","sha":"abc","repo":{"full_name":"alice/project"}}}]`)
		default:
			_, _ = fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Git = &fakeGit{results: map[string]string{"symbolic-ref --quiet HEAD": "feature"}}
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "view", "--json=number,title"}))
	require.JSONEq(t, `{"number":9,"title":"Feature"}`, out.String())
	deps.Git = &fakeGit{results: map[string]string{}}
	require.Equal(t, clierr.Usage, clierr.ExitCode(Execute(context.Background(), deps, []string{"pr", "view"})))
	err := Execute(context.Background(), deps, []string{"pr", "view", "-R", "alice/project"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "pass a pull request number, URL or branch with --repo")
}
