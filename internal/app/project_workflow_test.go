// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestProjectWorkflowCommandsManageWorkflowsOfAnOrganizationProject(t *testing.T) {
	var mutations []string
	const workflow = `{"id":7,"event":"item_column_changed","enabled":true,"filters":{"issue_type":"issue","source_column_id":10},"actions":{"add_label_ids":[20]}}`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/projects":
			writeFirstPage(w, r, `[{"id":3,"title":"Roadmap","state":"open"},{"id":4,"title":"Old","state":"closed"}]`)
		case "GET /api/v1/orgs/acme/projects/3/columns":
			writeFirstPage(w, r, `[{"id":10,"title":"Doing"},{"id":11,"title":"Done"}]`)
		case "GET /api/v1/orgs/acme/labels":
			writeFirstPage(w, r, `[{"id":20,"name":"bug"}]`)
		case "GET /api/v1/orgs/acme/projects/3/workflows":
			writeFirstPage(w, r, `[`+workflow+`,{"id":8,"event":"item_added_to_project","enabled":false,"actions":{"issue_state":"close"}}]`)
		case "GET /api/v1/orgs/acme/projects/4/workflows/8":
			_, _ = w.Write([]byte(`{"id":8,"event":"item_closed","enabled":false,"actions":{"column_id":11}}`))
		case "PATCH /api/v1/orgs/acme/projects/4/workflows/8":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"project is closed"}`))
		case "GET /api/v1/orgs/acme/projects/3/workflows/7":
			_, _ = w.Write([]byte(workflow))
		case "POST /api/v1/orgs/acme/projects/3/workflows", "PATCH /api/v1/orgs/acme/projects/3/workflows/7":
			mutations = append(mutations, r.Method+" "+r.URL.Path+" "+string(body))
			_, _ = w.Write([]byte(workflow))
		case "DELETE /api/v1/orgs/acme/projects/3/workflows/7":
			mutations = append(mutations, r.Method+" "+r.URL.Path)
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
		err := Execute(context.Background(), deps, append([]string{"project", "workflow", "--org", "acme"}, args...))
		return out.String(), err
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := execute(args...)
		require.NoError(t, err)
		return out
	}

	// Disabled workflows are listed with --all only, with the columns of the workflow named.
	require.Equal(t, "7\titem_column_changed\tenabled\tissues, from Doing\tadd label bug\n", run("list", "roadmap"))
	var listed []services.ProjectWorkflow
	require.NoError(t, json.Unmarshal([]byte(run("list", "Roadmap", "--all", "--json", "id,enabled")), &listed))
	require.Len(t, listed, 2)
	require.Contains(t, run("view", "3", "7"), "then:\tadd label bug\n")
	require.Contains(t, run("list", "Roadmap", "--all"), "8\titem_added_to_project\tdisabled\tany item\tclose\n")
	// Gitea refuses to change the workflows of a closed project with a 403.
	_, err := execute("enable", "Old", "8")
	require.ErrorContains(t, err, `project "Old" in acme is closed; reopen it with gt project reopen`)

	run("create", "Roadmap", "--event", "item_column_changed", "--from-column", "doing", "--issue-state", "close", "--add-label", "BUG", "--disabled")
	// Only the changed part is rebuilt, from the workflow as it is.
	run("edit", "Roadmap", "7", "--issue-type", "any", "--label", "20")
	run("enable", "Roadmap", "7")
	run("delete", "Roadmap", "7", "--yes")
	require.Equal(t, []string{
		`POST /api/v1/orgs/acme/projects/3/workflows {"event":"item_column_changed","enabled":false,"filters":{"source_column_id":10},"actions":{"add_label_ids":[20],"issue_state":"close"}}`,
		`PATCH /api/v1/orgs/acme/projects/3/workflows/7 {"filters":{"source_column_id":10,"label_ids":[20]}}`,
		"DELETE /api/v1/orgs/acme/projects/3/workflows/7",
	}, mutations)
}

func TestProjectWorkflowUsageErrorsPrecedeRequests(t *testing.T) {
	requireUsageBeforeRequests(t, "workflow creation requires --event", "project", "workflow", "create", "Roadmap", "--column", "Done")
	requireUsageBeforeRequests(t, "workflow creation requires an action", "project", "workflow", "create", "Roadmap", "--event", "item_closed", "--issue-state", "none")
	requireUsageBeforeRequests(t, "--from-column and --to-column require --event item_column_changed", "project", "workflow", "create", "Roadmap", "--event", "item_closed", "--from-column", "Doing", "--column", "Done")
	requireUsageBeforeRequests(t, "nothing to edit", "project", "workflow", "edit", "Roadmap", "7")
	requireUsageBeforeRequests(t, `invalid workflow number "x"`, "project", "workflow", "view", "Roadmap", "x")
	requireUsageBeforeRequests(t, "workflow 7 is selected more than once", "project", "workflow", "delete", "Roadmap", "7", "7", "--yes")
	requireUsageBeforeRequests(t, "label names cannot be empty", "project", "workflow", "edit", "Roadmap", "7", "--label", " ")
	requireUsageBeforeRequests(t, `--add-label "bug" cannot be combined with --remove-label "Bug"`, "project", "workflow", "create", "Roadmap", "--event", "item_closed", "--add-label", "bug", "--remove-label", "Bug")
}

func TestProjectWorkflowsAreUnsupportedBeforeGitea29(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/projects":
			writeFirstPage(w, r, `[{"id":3,"title":"Roadmap","state":"open"}]`)
		case "/api/v1/orgs/acme/projects/3/workflows", "/api/v1/orgs/acme/projects/3/workflows/7":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"project", "workflow", "view", "Roadmap", "7", "--org", "acme"})
	require.ErrorContains(t, err, "project workflows is unsupported")
	deps, _, _ = serverDeps(t, server.URL)
	err = Execute(context.Background(), deps, []string{"project", "workflow", "list", "Roadmap", "--org", "acme"})
	require.ErrorContains(t, err, "project workflows is unsupported")
	require.ErrorContains(t, err, "from 29.0")
	deps, _, _ = serverDeps(t, server.URL)
	err = Execute(context.Background(), deps, []string{"project", "workflow", "delete", "Roadmap", "7", "--org", "acme", "--yes"})
	require.ErrorContains(t, err, "project workflows is unsupported")
}

func TestProjectWorkflowsRequireGitea29(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.ServerVersion = func(context.Context, *transport.Client) (string, error) { return "28.3.0", nil }
	err := Execute(context.Background(), deps, []string{"project", "workflow", "list", "Roadmap", "--org", "acme"})
	require.ErrorContains(t, err, "gt project workflow list requires Gitea 29 or newer")
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
}
