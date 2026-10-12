// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryCommandsAcceptPositionalRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			requirePayload(t, `{"archived":true}`, r.Body)
		}
		_, _ = w.Write([]byte(`{"full_name":"bob/tool","archived":true}`))
	}))
	defer server.Close()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"view slug", []string{"repo", "view", "bob/tool"}, "GET /api/v1/repos/bob/tool"},
		{"view URL", []string{"repo", "view", server.URL + "/bob/tool"}, "GET /api/v1/repos/bob/tool"},
		{"archive", []string{"repo", "archive", "bob/tool", "--yes"}, "PATCH /api/v1/repos/bob/tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests = nil
			deps, out, _ := testDeps(t)
			deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
			require.NoError(t, Execute(context.Background(), deps, append(test.args, "--no-input", "--json=full_name")))
			require.Equal(t, []string{test.want}, requests)
			require.JSONEq(t, `{"full_name":"bob/tool"}`, out.String())
		})
	}
}
