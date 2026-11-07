// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

func TestProjectCommandsManageProjectsOfAnOrganization(t *testing.T) {
	var mutations []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/projects":
			writeFirstPage(w, r, `[{"id":3,"title":"Roadmap","state":"open","type":"organization","num_open_issues":1,"num_closed_issues":3,"html_url":"https://example.test/org/acme/-/projects/3"},{"id":4,"title":"Old","state":"closed"}]`)
		case "POST /api/v1/orgs/acme/projects":
			body, _ := io.ReadAll(r.Body)
			mutations = append(mutations, "POST "+string(body))
			_, _ = w.Write([]byte(`{"id":5,"title":"New","html_url":"https://example.test/org/acme/-/projects/5"}`))
		case "PATCH /api/v1/orgs/acme/projects/3", "PATCH /api/v1/orgs/acme/projects/4":
			body, _ := io.ReadAll(r.Body)
			mutations = append(mutations, "PATCH "+r.URL.Path+" "+string(body))
			_, _ = w.Write([]byte(`{"id":3,"title":"Roadmap","state":"closed"}`))
		case "DELETE /api/v1/orgs/acme/projects/4":
			mutations = append(mutations, "DELETE "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	execute := func(args ...string) (string, error) {
		t.Helper()
		deps, out, _ := serverDeps(t, server.URL)
		err := Execute(context.Background(), deps, append([]string{"project", "--org", "acme"}, args...))
		return out.String(), err
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := execute(args...)
		require.NoError(t, err)
		return out
	}

	var listed []services.Project
	require.NoError(t, json.Unmarshal([]byte(run("list", "--state", "all", "--json", "id,title,open_issues,created_at")), &listed))
	require.Equal(t, []int64{3, 4}, []int64{listed[0].ID, listed[1].ID})
	require.Equal(t, int64(1), listed[0].OpenIssues)
	require.Empty(t, listed[1].CreatedAt)

	// Titles address projects, so a second project or a retitle to an existing title is refused.
	_, err := execute("create", "roadmap")
	require.ErrorContains(t, err, "pass --force to update it")
	_, err = execute("edit", "Old", "--title", " ROADMAP ")
	require.ErrorContains(t, err, `project "Roadmap"`)

	var viewed services.Project
	require.NoError(t, json.Unmarshal([]byte(run("view", "roadmap", "--json", "id,title")), &viewed))
	require.Equal(t, "Roadmap", viewed.Title)

	require.Equal(t, "https://example.test/org/acme/-/projects/5\n", run("create", "New", "--template", "basic_kanban"))
	run("edit", "3", "--description", "Planned")
	run("create", "--title", "Roadmap", "--description", "Planned", "--force")
	run("close", "Roadmap", "Old")
	run("delete", "Old", "--yes")
	require.Equal(t, []string{
		`POST {"title":"New","template_type":"basic_kanban"}`,
		`PATCH /api/v1/orgs/acme/projects/3 {"description":"Planned"}`,
		`PATCH /api/v1/orgs/acme/projects/3 {"description":"Planned"}`,
		// Old is closed already, so only Roadmap is sent.
		`PATCH /api/v1/orgs/acme/projects/3 {"state":"closed"}`,
		"DELETE /api/v1/orgs/acme/projects/4",
	}, mutations)
}

func TestProjectCommandsManageProjectsOfYourAccount(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/user/projects":
			writeFirstPage(w, r, `[{"id":7,"title":"Personal","state":"open","type":"individual"}]`)
		case "POST /api/v1/user/projects":
			_, _ = w.Write([]byte(`{"id":8,"title":"Reading","html_url":"https://example.test/alice/-/projects/8"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"project", "list", "--account", "--json", "id"}))
	require.JSONEq(t, `[{"id":7}]`, out.String())
	deps, out, _ = serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"project", "create", "Reading", "--account"}))
	require.Equal(t, "https://example.test/alice/-/projects/8\n", out.String())
	require.Contains(t, requests, "POST /api/v1/user/projects")
}

func TestProjectUsageErrorsPrecedeRequests(t *testing.T) {
	requireUsageBeforeRequests(t, "--account cannot be combined with --org", "project", "list", "--account", "--org", "acme")
	requireUsageBeforeRequests(t, "--account cannot be combined with --repo", "project", "list", "--account", "-R", "alice/project")
	requireUsageBeforeRequests(t, "nothing to edit", "project", "edit", "Roadmap")
	requireUsageBeforeRequests(t, "project title cannot be empty", "project", "create", " ")
	requireUsageBeforeRequests(t, "a title argument cannot be combined with --title", "project", "create", "Roadmap", "--title", "Plans")
	requireUsageBeforeRequests(t, "--org cannot be combined with --repo", "project", "list", "--org", "acme", "-R", "alice/project")
}
