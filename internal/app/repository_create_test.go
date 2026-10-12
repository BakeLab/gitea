// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestRepositoryCreateGrantsTeamAccess(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/teams":
			// The server caps pages below the requested limit (MAX_RESPONSE_ITEMS=1).
			pages := map[string]string{"1": `[{"id":4,"name":"Owners"}]`, "2": `[{"id":3,"name":"Developers"}]`}
			_, _ = w.Write([]byte(cmp.Or(pages[r.URL.Query().Get("page")], `[]`)))
		case "POST /api/v1/orgs/acme/repos":
			_, _ = w.Write([]byte(`{"name":"project","full_name":"acme/project"}`))
		case "PUT /api/v1/teams/3/repos/acme/project":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "developers", "--no-input", "--json=full_name"}))
	teamPages := []string{"GET /api/v1/orgs/acme/teams?limit=50&page=1", "GET /api/v1/orgs/acme/teams?limit=50&page=2", "GET /api/v1/orgs/acme/teams?limit=50&page=3"}
	require.Equal(t, append(teamPages, "POST /api/v1/orgs/acme/repos", "PUT /api/v1/teams/3/repos/acme/project"), requests)
	require.JSONEq(t, `{"full_name":"acme/project"}`, out.String())
	requests = nil
	out.Reset()
	err := Execute(context.Background(), deps, []string{"repo", "create", "project", "--org", "acme", "--team", "missing", "--no-input"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, `team "missing" not found in organization acme`)
	require.Equal(t, teamPages, requests)
	require.Empty(t, out.String())
}

func TestRepositoryCreateClonesNewRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"id":7,"name":"project","full_name":"alice/project","clone_url":%q}`, "http://"+r.Host+"/alice/project.git")
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	call := credentialCall(t, deps, server.URL, "alice", "clone", "--", server.URL+"/alice/project.git")
	runner := &fakeGit{results: map[string]string{call: ""}}
	deps.Git = runner
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "create", "project", "--clone", "--no-input", "--json=id,full_name"}))
	require.Equal(t, []string{"POST /api/v1/user/repos"}, requests)
	require.Equal(t, []string{call}, runner.calls)
	require.JSONEq(t, `{"id":7,"full_name":"alice/project"}`, out.String())
}
