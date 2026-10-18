// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPullEditPreservesOmittedFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flags   []string
		current string
		patch   string
		reads   int
	}{
		{"ready reads existing title", []string{"--ready"}, "[wIp] existing", `{"title":"existing"}`, 1},
		{"draft is idempotent", []string{"--draft", "--title", "wip: already"}, "", `{"title":"wip: already"}`, 0},
		{"explicit empty and false", []string{"--body=", "--allow-maintainer-edit=false"}, "", `{"body":"","allow_maintainer_edit":false}`, 0},
		{"base only", []string{"--base", "stable"}, "", `{"base":"stable"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, writes := 0, 0
			server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/repos/alice/project/pulls/7", r.URL.Path)
				if r.Method == http.MethodGet {
					reads++
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"number": 7, "title": tc.current}))
					return
				}
				require.Equal(t, http.MethodPatch, r.Method)
				writes++
				requirePayload(t, tc.patch, r.Body)
				_, _ = w.Write([]byte(`{"number":7,"title":"result"}`))
			}))
			defer server.Close()
			deps, out, diagnostics := serverDeps(t, server.URL)
			args := append([]string{"pr", "edit", "7", "-R", "alice/project", "--json=number"}, tc.flags...)
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.reads, reads)
			require.Equal(t, 1, writes)
			require.JSONEq(t, `{"number":7}`, out.String())
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestPullEditAddsAssigneesWithoutExtraReads(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"number":7,"assignees":[{"login":"alice"}]}`))
			return
		}
		requirePayload(t, `{"assignees":["alice","bob"]}`, r.Body)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "edit", "7", "-R", "alice/project", "--add-assignee", "bob,Alice", "--json=number"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/issues/7", "PATCH /api/v1/repos/alice/project/pulls/7"}, calls)
}

func TestPullReadyFailsWhenGiteaStillSeesADraft(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"number":7,"title":"Draft: x","draft":true}`))
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "ready", "7", "-R", "alice/project"})
	require.ErrorContains(t, err, "still treats the pull request as a draft")
	require.NotContains(t, diagnostics.String(), "Marked")
}
