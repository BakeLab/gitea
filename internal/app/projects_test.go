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
	// Two spellings of a title load the project once.
	reads := map[string]int{}
	handler := projectServer(t, &mutations)
	counting := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			reads[r.URL.Path]++
		}
		handler.ServeHTTP(w, r)
	}))
	defer counting.Close()
	deps, _, _ = serverDeps(t, counting.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--remove-project", "Board", "--remove-project", "board"}))
	require.Equal(t, 1, reads["/api/v1/orgs/acme/projects/3/columns"])
	require.Equal(t, 1, reads["/api/v1/orgs/acme/projects/3/columns/11/issues"])
	mutations = nil
	err = Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "Board", "--remove-project", "board"})
	require.EqualError(t, err, `--add-project "Board" cannot be combined with --remove-project "board", which name the same project`)
	require.Empty(t, mutations)
	requireUsageBeforeRequests(t, "project names cannot be empty", "issue", "create", "--title", "x", "--project", " ")
	requireUsageBeforeRequests(t, "--project cannot be combined with --web", "issue", "create", "--title", "x", "--project", "Board", "--web")
}

func TestIssueEditTellsProjectsApartThatDifferOnlyInCase(t *testing.T) {
	var mutations []string
	projects := projectServer(t, &mutations)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/projects":
			writeFirstPage(w, r, `[{"id":3,"title":"Board"},{"id":6,"title":"board"}]`)
		case "GET /api/v1/orgs/acme/projects/6/columns":
			writeFirstPage(w, r, `[{"id":12,"title":"Todo","default":true}]`)
		case "GET /api/v1/orgs/acme/projects/6/columns/12/issues":
			writeFirstPage(w, r, `[{"id":70,"number":12}]`)
		case "DELETE /api/v1/orgs/acme/projects/6/columns/12/issues/70":
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			projects.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "Board", "--remove-project", "board"}))
	require.Equal(t, []string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70", "DELETE /api/v1/orgs/acme/projects/6/columns/12/issues/70"}, mutations)
}

func TestIssueEditNamesProjectsByIDBeforeTitle(t *testing.T) {
	var mutations []string
	projects := projectServer(t, &mutations)
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/acme/app/projects":
			if r.URL.Query().Get("state") == "closed" {
				writeFirstPage(w, r, `[]`)
			} else {
				writeFirstPage(w, r, `[{"id":7,"title":"3"},{"id":8,"title":"2027"}]`)
			}
		case "GET /api/v1/repos/acme/app/projects/8/columns":
			writeFirstPage(w, r, `[{"id":12,"title":"Todo","default":true}]`)
		case "GET /api/v1/repos/acme/app/projects/8/columns/12/issues":
			writeFirstPage(w, r, `[]`)
		case "POST /api/v1/repos/acme/app/projects/8/columns/12/issues/70":
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			projects.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	// 3 is the ID of the organization's "Board" before it is the title of a repository project.
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "3", "--add-project", "2027"}))
	require.Equal(t, []string{"POST /api/v1/orgs/acme/projects/3/columns/11/issues/70", "POST /api/v1/repos/acme/app/projects/8/columns/12/issues/70"}, mutations)
	err := Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "5"})
	require.ErrorContains(t, err, `project "5" is closed`)
	err = Execute(context.Background(), deps, []string{"issue", "edit", "12", "-R", "acme/app", "--add-project", "99"})
	require.ErrorContains(t, err, `project "99" not found in acme/app`)
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

func TestIssueEditAddsToProjectsOfTheUserOwningTheRepository(t *testing.T) {
	var mutations []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/notes/issues/3", "GET /api/v1/repos/bob/notes/issues/3":
			_, _ = w.Write([]byte(`{"id":71,"number":3,"title":"Todo"}`))
		case "GET /api/v1/repos/alice/notes/projects", "GET /api/v1/repos/bob/notes/projects":
			writeFirstPage(w, r, `[]`)
		case "GET /api/v1/user/projects":
			writeFirstPage(w, r, `[{"id":8,"title":"Personal"}]`)
		case "GET /api/v1/user/projects/8/columns":
			writeFirstPage(w, r, `[{"id":30,"title":"Backlog","default":true}]`)
		case "GET /api/v1/user/projects/8/columns/30/issues":
			writeFirstPage(w, r, `[]`)
		case "POST /api/v1/user/projects/8/columns/30/issues/71":
			mutations = append(mutations, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/orgs/alice/projects", "GET /api/v1/orgs/bob/projects":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "3", "-R", "alice/notes", "--add-project", "Personal"}))
	require.Equal(t, []string{"POST /api/v1/user/projects/8/columns/30/issues/71"}, mutations)
	// Gitea lets only the user who owns a repository put its issues on their projects.
	err := Execute(context.Background(), deps, []string{"issue", "edit", "3", "-R", "bob/notes", "--add-project", "Personal"})
	require.ErrorContains(t, err, `project "Personal" not found in bob/notes`)
}
