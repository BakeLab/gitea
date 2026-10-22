// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestTeamEditRefusesToRenameTheOwnerTeam(t *testing.T) {
	var patched int
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patched++
		}
		_, _ = w.Write([]byte(`{"id":3,"name":"Owners","permission":"owner","organization":{"username":"acme"}}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"team", "edit", "3", "--name", "Admins"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, "owner team and cannot be renamed")
	require.Zero(t, patched)
	err = Execute(context.Background(), deps, []string{"team", "edit", "3", "--permission", "read", "--unit", "repo.wiki=read"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.EqualError(t, err, "team acme/Owners is the organization's owner team, whose access cannot be changed; drop --permission and --unit")
	err = Execute(context.Background(), deps, []string{"team", "edit", "3", "--all-repositories=false"})
	require.ErrorContains(t, err, "drop --all-repositories")
	require.Zero(t, patched)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "edit", "3", "--description", "Owners", "--json"}))
	require.Equal(t, 1, patched)
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

func TestTeamListCountsOnlyShortPages(t *testing.T) {
	teams := make([]string, teamCountLimit+1)
	for i := range teams {
		teams[i] = fmt.Sprintf(`{"id":%d,"name":"team%d","permission":"read"}`, i+1, i+1)
	}
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		_, _ = w.Write([]byte("[" + strings.Join(teams, ",") + "]"))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "list", "acme"}))
	require.Equal(t, []string{"/api/v1/orgs/acme/teams"}, requests, "a long page reads no member or repository counts")
	require.True(t, strings.HasPrefix(out.String(), "1\tteam1\tread\n"), out.String())
}

func TestTeamPermissionSetsEveryUnit(t *testing.T) {
	var sent []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			sent = append(sent, body)
		}
		permission := "none"
		if len(sent) > 0 {
			permission, _ = sent[len(sent)-1]["permission"].(string)
		}
		_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"` + permission + `","organization":{"username":"acme"}}`))
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
	require.Len(t, sent, 3)
	require.Equal(t, "devs", sent[0]["name"])
	require.Equal(t, every("write"), sent[0]["units_map"])
	// The permission goes first on its own, because Gitea 28 keeps an admin team admin otherwise.
	require.Equal(t, map[string]any{"name": "devs", "permission": "read"}, sent[1])
	wikiWrite := every("read")
	wikiWrite["repo.wiki"] = "write"
	require.Equal(t, map[string]any{"permission": "read", "units_map": wikiWrite}, sent[2])
}

func TestTeamEditComparesTheEffectiveAccess(t *testing.T) {
	// Gitea 28 reports permission none for a team whose units grant its access.
	response := `{"id":5,"name":"devs","permission":"none","units_map":{"repo.code":"read","repo.issues":"read","repo.pulls":"read","repo.releases":"read","repo.wiki":"read","repo.projects":"read","repo.packages":"read","repo.actions":"read"}}`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "edit", "5", "--permission", "read", "--json", "permission,access"}))
	require.JSONEq(t, `{"permission":"none","access":"read"}`, out.String())

	response = `{"id":5,"name":"devs","permission":"admin"}`
	err := Execute(context.Background(), deps, []string{"team", "edit", "5", "--permission", "write"})
	require.ErrorContains(t, err, "its access is admin instead of the requested write")
}

func TestTeamMemberRemoveConfirmsOnce(t *testing.T) {
	var removed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/teams/5":
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","organization":{"username":"acme"}}`))
		case "GET /api/v1/teams/5/members/carol", "GET /api/v1/teams/5/members/dave", "GET /api/v1/users/erin":
			_, _ = w.Write([]byte(`{"login":"` + path.Base(r.URL.Path) + `"}`))
		case "GET /api/v1/teams/5/members/erin":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
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
	require.NoError(t, Execute(context.Background(), deps, []string{"team", "member", "remove", "5", "carol", "erin", "dave"}))
	require.Equal(t, []string{"Remove carol and dave from team acme/devs?"}, prompt.confirmations)
	require.Equal(t, []string{"carol", "dave"}, removed)
	require.Equal(t, "! erin is not a member of team acme/devs\n✓ Removed carol from team acme/devs\n✓ Removed dave from team acme/devs\n", withoutANSI(diagnostics.String()))
}

func TestTeamMembershipAndAccessChangesContinuePastFailures(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/teams/5":
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","organization":{"username":"acme"}}`))
		case path.Base(r.URL.Path) == "ghost":
			http.NotFound(w, r)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"login":"bob"}`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		args       []string
		changed    string
		done, fail string
	}{
		{[]string{"member", "add"}, "PUT /api/v1/teams/5/members/bob", "Added bob to team acme/devs", "user ghost: not found"},
		{[]string{"member", "remove"}, "DELETE /api/v1/teams/5/members/bob", "Removed bob from team acme/devs", "user ghost: not found"},
		{[]string{"repo", "add"}, "PUT /api/v1/teams/5/repos/acme/bob", "Granted team acme/devs access to acme/bob", "repository acme/ghost: not found"},
		{[]string{"repo", "remove"}, "DELETE /api/v1/teams/5/repos/acme/bob", "Revoked team acme/devs access to acme/bob", "repository acme/ghost: not found"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			changed = nil
			deps, _, diagnostics := serverDeps(t, server.URL)
			deps.Streams.OutTTY = true
			err := Execute(context.Background(), deps, append([]string{"team"}, append(tc.args, "5", "ghost", "bob", "--yes")...))
			require.ErrorContains(t, err, tc.fail)
			require.Equal(t, []string{tc.changed}, changed, "the failure of ghost does not stop bob")
			require.Equal(t, "✓ "+tc.done+"\n", diagnostics.String())
		})
	}
}
