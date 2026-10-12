// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestBulkMilestoneStateContinuesAfterFailureAndKeepsJSONClean(t *testing.T) {
	mutations := []string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/milestones" {
			writeMilestones(w, r, `[{"id":9,"title":"v9"},{"id":10,"title":"v10"},{"id":11,"title":"v11"}]`)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/alice/project/milestones/")
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprintf(w, `{"id":%s,"title":"v%s"}`, id, id)
			return
		}
		mutations = append(mutations, id)
		if id == "10" {
			w.WriteHeader(403)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%s,"title":"Milestone %s","state":"closed"}`, id, id)
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"milestone", "close", "9", "10", "11", "-R", "alice/project", "--json=id,state"})
	require.ErrorContains(t, err, "milestone v10")
	require.Equal(t, clierr.Failure, clierr.ExitCode(err))
	require.Equal(t, []string{"9", "10", "11"}, mutations)
	require.JSONEq(t, `[{"id":9,"state":"closed"},{"id":11,"state":"closed"}]`, out.String())
	require.Empty(t, diagnostics.String())
	mutations = nil
	out.Reset()
	err = Execute(context.Background(), deps, []string{"milestone", "close", "9", "bad", "-R", "alice/project"})
	require.ErrorContains(t, err, `milestone "bad" not found in alice/project`)
	require.Empty(t, mutations)
	require.Empty(t, out.String())
}
