// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// projectServer serves an organization project "Board" with a default column 11, to which the issue
// with the global ID 70 can be added, a project "Empty" without columns and a closed project "Old".
func projectServer(t *testing.T, mutations *[]string) http.Handler {
	onBoard := false
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/acme/app/issues/12":
			_, _ = w.Write([]byte(`{"id":70,"number":12,"title":"Crash"}`))
		case "GET /api/v1/repos/acme/app/projects":
			writeFirstPage(w, r, `[]`)
		case "GET /api/v1/orgs/acme/projects":
			if r.URL.Query().Get("state") == "closed" {
				writeFirstPage(w, r, `[{"id":5,"title":"Old","state":"closed"}]`)
			} else {
				writeFirstPage(w, r, `[{"id":3,"title":"Board"},{"id":4,"title":"Empty"}]`)
			}
		case "GET /api/v1/orgs/acme/projects/4/columns":
			writeFirstPage(w, r, `[]`)
		case "POST /api/v1/orgs/acme/projects/4/columns":
			*mutations = append(*mutations, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":20,"title":"Uncategorized"}`))
		case "POST /api/v1/orgs/acme/projects/4/columns/20/default", "POST /api/v1/orgs/acme/projects/4/columns/20/issues/70":
			*mutations = append(*mutations, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/orgs/acme/projects/3/columns":
			writeFirstPage(w, r, `[{"id":10,"title":"Done"},{"id":11,"title":"Backlog","default":true}]`)
		case "GET /api/v1/orgs/acme/projects/3/columns/10/issues":
			writeFirstPage(w, r, `[]`)
		case "GET /api/v1/orgs/acme/projects/3/columns/11/issues":
			if onBoard {
				writeFirstPage(w, r, `[{"id":70,"number":12}]`)
			} else {
				writeFirstPage(w, r, `[]`)
			}
		case "POST /api/v1/orgs/acme/projects/3/columns/11/issues/70", "DELETE /api/v1/orgs/acme/projects/3/columns/11/issues/70":
			*mutations = append(*mutations, r.Method+" "+r.URL.Path)
			onBoard = r.Method == http.MethodPost
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestIssueEditAddsToAndRemovesFromProjects(t *testing.T) {
	var mutations []string
	server := newAuthenticatedServer(projectServer(t, &mutations))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "board"}))
	// An issue already on the board keeps its column.
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "Board"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--remove-project", "Board"}))
	require.Equal(t, []string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70", "DELETE /api/v1/orgs/acme/projects/3/columns/11/issues/70"}, mutations)
	err := Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "Roadmap"})
	require.ErrorContains(t, err, `project "Roadmap" not found`)
	err = Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "old"})
	require.ErrorContains(t, err, `project "old" is closed`)
	// A board without columns gets the default column Gitea creates on the web, once.
	mutations = nil
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "Empty", "--add-project", "empty"}))
	require.Equal(t, []string{"POST /api/v1/orgs/acme/projects/4/columns", "POST /api/v1/orgs/acme/projects/4/columns/20/default", "POST /api/v1/orgs/acme/projects/4/columns/20/issues/70"}, mutations)
	requireUsageBeforeRequests(t, "project names cannot be empty", "issue", "create", "--title", "x", "--project", " ")
	requireUsageBeforeRequests(t, "--project cannot be combined with --web", "issue", "create", "--title", "x", "--project", "Board", "--web")
}

func TestPullEditChangesProjectsOnlyOnceTheEditSucceeded(t *testing.T) {
	var mutations []string
	projects, rejectEdit := projectServer(t, &mutations), true
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/acme/app/pulls/12" && rejectEdit:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"invalid title"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/acme/app/pulls/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Fix"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/acme/app/pulls/12":
			_, _ = w.Write([]byte(`{"number":12,"title":"Crash"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/orgs/acme/projects/3/columns/11/issues/70" && !rejectEdit:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			projects.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"pr", "edit", "12", "-R", "acme/app", "--title", "Fix", "--add-project", "Board"})
	require.ErrorContains(t, err, "invalid title")
	require.Empty(t, mutations)
	// A project that cannot be changed after the edit is reported as a follow-up failure.
	rejectEdit = false
	err = Execute(context.Background(), deps, []string{"pr", "edit", "12", "-R", "acme/app", "--title", "Fix", "--add-project", "Board", "--json", "title"})
	require.ErrorContains(t, err, `pull request #12 was edited, but follow-up steps failed: add to project "Board"`)
	require.JSONEq(t, `{"title":"Fix"}`, out.String())
}
