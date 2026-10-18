// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestTeamDeleteNamesTheTeamAndProtectsTheOwnerTeam(t *testing.T) {
	var deleted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/teams/3":
			_, _ = w.Write([]byte(`{"id":3,"name":"Owners","permission":"owner","organization":{"username":"acme"}}`))
		case "GET /api/v1/teams/5":
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"write","organization":{"username":"acme"}}`))
		case "DELETE /api/v1/teams/3", "DELETE /api/v1/teams/5":
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"team", "delete", "3", "--yes"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, `team "Owners" (ID 3) from acme is the organization's owner team`)
	require.ErrorContains(t, err, "--force")
	require.Empty(t, deleted)

	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{inputs: map[string]string{`Delete owner team "Owners" (ID 3) from acme? Its members lose owner access. Type acme/Owners to confirm:`: "Owners"}}
	deps.Prompter = prompt
	err = Execute(context.Background(), deps, []string{"team", "delete", "3", "--force"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
	require.Len(t, prompt.asked, 1)
	require.Empty(t, deleted)
	prompt.inputs[prompt.asked[0]] = "acme/Owners"
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "delete", "3", "--force"}))
	require.Equal(t, []string{"/api/v1/teams/3"}, deleted)

	prompt.accept = true
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "delete", "5"}))
	require.Equal(t, []string{`Delete team "devs" (ID 5) from acme?`}, prompt.confirmations)
	require.Equal(t, []string{"/api/v1/teams/3", "/api/v1/teams/5"}, deleted)
}

func TestTeamEditMergesUnitsIntoTheCurrentPolicy(t *testing.T) {
	var sent []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/teams/5", r.URL.Path)
		if r.Method == http.MethodPatch {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			sent = append(sent, body)
		}
		_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"write","units_map":{"repo.code":"write","repo.issues":"write","repo.pulls":"write"}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "edit", "5", "--unit", "repo.wiki=write", "--json"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "edit", "5", "--unit", "repo.wiki=write", "--unit-replace", "--json"}))
	require.Equal(t, []map[string]any{
		{"units_map": map[string]any{"repo.code": "write", "repo.issues": "write", "repo.pulls": "write", "repo.wiki": "write"}},
		{"units_map": map[string]any{"repo.wiki": "write"}},
	}, sent)
	err := Execute(context.Background(), deps, []string{"team", "edit", "5", "--unit-replace", "--name", "devs"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "--unit-replace requires --unit")
	require.Len(t, sent, 2)
}

func TestTeamRepoAddTakesRepositoriesAsArguments(t *testing.T) {
	var granted []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/teams/5":
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"none","organization":{"username":"acme"}}`))
		case "PUT /api/v1/teams/5/repos/acme/service", "PUT /api/v1/teams/5/repos/acme/website":
			granted = append(granted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "repo", "add", "5", "service", "acme/website"}))
	require.Equal(t, []string{"/api/v1/teams/5/repos/acme/service", "/api/v1/teams/5/repos/acme/website"}, granted)
	for args, want := range map[string]string{
		"alice/project":           "alice/project is not owned by organization acme; teams can only be granted repositories of their organization",
		"service -R acme/service": "--repo is not used by gt team repo add; name the repositories as arguments",
	} {
		err := Execute(context.Background(), deps, append([]string{"team", "repo", "add", "5"}, strings.Fields(args)...))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
		require.ErrorContains(t, err, want)
	}
	require.Len(t, granted, 2)
}

func TestTeamsAreAddressedAsOrganizationAndName(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/orgs/acme/teams/search", r.URL.Path)
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`{"ok":true,"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":[{"id":2,"name":"developers","permission":"none","units_map":{"repo.code":"write"}},{"id":7,"name":"developers-old","permission":"none"}]}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, ref := range []string{"acme/Developers", "acme/developers", "acme/2"} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, []string{"team", "view", ref, "--json", "id,name,organization"}))
		require.JSONEq(t, `{"id":2,"name":"developers","organization":"acme"}`, out.String(), ref)
	}
	err := Execute(context.Background(), deps, []string{"team", "view", "acme/ops"})
	require.EqualError(t, err, `team "ops" not found in acme`)
}

func TestTeamViewPrintsTheIDWhenPiped(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/teams/search":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`{"ok":true,"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"data":[{"id":2,"name":"developers","permission":"none"}]}`))
		case "/api/v1/teams/2/members", "/api/v1/teams/2/repos":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "view", "acme/developers"}))
	require.Contains(t, out.String(), "\nid:\t2\n")
}

func TestTeamListShowsAccessAndSizes(t *testing.T) {
	totals := map[string]string{"/api/v1/teams/1/members": "1", "/api/v1/teams/1/repos": "4", "/api/v1/teams/2/members": "3", "/api/v1/teams/2/repos": "2"}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/orgs/acme/teams" {
			_, _ = w.Write([]byte(`[{"id":1,"name":"Owners","permission":"owner","includes_all_repositories":true},
				{"id":2,"name":"developers","permission":"none","units_map":{"repo.code":"write","repo.issues":"write","repo.pulls":"write"}}]`))
			return
		}
		total, ok := totals[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("X-Total-Count", total)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "list", "acme"}))
	require.Equal(t, "1\tOwners\towner\t1\tall\n2\tdevelopers\twrite (code, issues, pulls)\t3\t2\n", out.String())
}

func TestTeamPermissionSetsEveryUnit(t *testing.T) {
	var sent []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			sent = append(sent, body)
		}
		_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"none","organization":{"username":"acme"}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	every := func(access string) map[string]any {
		units := map[string]any{"repo.ext_issues": "read", "repo.ext_wiki": "read"}
		for _, unit := range []string{"repo.code", "repo.issues", "repo.pulls", "repo.releases", "repo.wiki", "repo.projects", "repo.packages", "repo.actions"} {
			units[unit] = access
		}
		return units
	}
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "create", "acme/devs", "--permission", "write"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "edit", "5", "--permission", "read", "--unit", "repo.wiki=write"}))
	require.Len(t, sent, 2)
	require.Equal(t, "devs", sent[0]["name"])
	require.Equal(t, every("write"), sent[0]["units_map"])
	wikiWrite := every("read")
	wikiWrite["repo.wiki"] = "write"
	require.Equal(t, map[string]any{"permission": "read", "units_map": wikiWrite}, sent[1])
}

func TestTeamMemberRemoveConfirmsOnce(t *testing.T) {
	var removed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/teams/5":
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","organization":{"username":"acme"}}`))
		case "DELETE /api/v1/teams/5/members/carol", "DELETE /api/v1/teams/5/members/dave":
			removed = append(removed, path.Base(r.URL.Path))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, true
	prompt := &scriptedPrompter{accept: true}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "member", "remove", "5", "carol", "dave"}))
	require.Equal(t, []string{"Remove carol and dave from team acme/devs?"}, prompt.confirmations)
	require.Equal(t, []string{"carol", "dave"}, removed)
	require.Equal(t, "✓ Removed carol from team acme/devs\n✓ Removed dave from team acme/devs\n", withoutANSI(diagnostics.String()))
}
