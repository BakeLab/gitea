// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestTeamRepositoryPolicyPreservesUnitPermissions(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/teams/1", r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":1,"permission":"read","units_map":{"repo.code":"write","repo.issues":"read"}}`))
			return
		}
		mutations++
		body := requestJSON(t, r)
		require.Equal(t, "read", body["permission"])
		require.Equal(t, false, body["includes_all_repositories"])
		require.Equal(t, map[string]any{"repo.code": "write", "repo.issues": "read"}, body["units_map"])
		require.NotContains(t, body, "name")
		require.NotContains(t, body, "description")
		_, _ = w.Write([]byte(`{"id":1,"permission":"read","organization":{"username":"acme"}}`))
	}))
	service := Teams{Client: client}
	allRepos := false
	item, err := service.Edit(context.Background(), 1, TeamOptions{AllRepositories: &allRepos})
	require.NoError(t, err)
	require.Equal(t, "acme", item.Organization)
	require.Equal(t, 1, mutations)
}

func TestTeamEditMergesUnitsUnlessReplaced(t *testing.T) {
	for _, test := range []struct {
		replace bool
		reads   int
		want    map[string]any
	}{
		{false, 1, map[string]any{"repo.code": "write", "repo.issues": "read", "repo.wiki": "write"}},
		{true, 0, map[string]any{"repo.wiki": "write"}},
	} {
		reads := 0
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/v1/teams/5", r.URL.Path)
			if r.Method == http.MethodGet {
				reads++
				_, _ = w.Write([]byte(`{"id":5,"permission":"write","units_map":{"repo.code":"write","repo.issues":"read","repo.wiki":"none"}}`))
				return
			}
			body := requestJSON(t, r)
			require.Equal(t, test.want, body["units_map"])
			require.NotContains(t, body, "permission")
			_, _ = w.Write([]byte(`{"id":5}`))
		}))
		_, err := (Teams{Client: client}).Edit(context.Background(), 5, TeamOptions{Units: map[string]string{"repo.wiki": "write"}, ReplaceUnits: test.replace})
		require.NoError(t, err)
		require.Equal(t, test.reads, reads)
	}
}

func TestTeamCreateSendsPermissionAndUnits(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/orgs/acme/teams", r.URL.Path)
		body := requestJSON(t, r)
		require.Equal(t, "Developers", body["name"])
		require.Equal(t, "write", body["permission"])
		require.Equal(t, true, body["can_create_org_repo"])
		require.Equal(t, false, body["includes_all_repositories"])
		require.Equal(t, map[string]any{"repo.code": "write"}, body["units_map"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":3,"name":"Developers","permission":"write","can_create_org_repo":true,"units_map":{"repo.code":"write"},"organization":{"name":"acme"}}`))
	}))
	name, permission, createRepo, allRepos := "Developers", "write", true, false
	item, err := (Teams{Client: client}).Create(context.Background(), "acme", TeamOptions{
		Name: &name, Permission: &permission, CanCreateRepo: &createRepo, AllRepositories: &allRepos, Units: map[string]string{"repo.code": "write"},
	})
	require.NoError(t, err)
	require.Equal(t, Team{ID: 3, Name: "Developers", Organization: "acme", Permission: "write", CanCreateRepo: true, Units: map[string]string{"repo.code": "write"}}, item)
}

func TestTeamAccessRequests(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.URL.Path == "/api/v1/teams/3/members":
			_, _ = w.Write([]byte(`[{"id":2,"login":"bob"}]`))
		case r.URL.Path == "/api/v1/teams/3/repos":
			_, _ = w.Write([]byte(`[{"id":4,"full_name":"acme/project","html_url":"https://gitea.test/acme/project"}]`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	service := Teams{Client: client}
	members, err := service.Members(context.Background(), 3, 5, 2)
	require.NoError(t, err)
	require.Equal(t, []TeamMember{{ID: 2, Username: "bob"}}, members.Items)
	repos, err := service.Repositories(context.Background(), 3, 5, 1)
	require.NoError(t, err)
	require.Equal(t, "https://gitea.test/acme/project", repos.Items[0].URL)
	require.NoError(t, service.SetMember(context.Background(), 3, "bob", false))
	require.NoError(t, service.SetRepository(context.Background(), 3, "acme/project", true))
	require.Equal(t, []string{
		"GET /api/v1/teams/3/members?limit=5&page=2", "GET /api/v1/teams/3/repos?limit=5&page=1",
		"DELETE /api/v1/teams/3/members/bob", "PUT /api/v1/teams/3/repos/acme/project",
	}, requests)
}

func TestTeamFromKeepsAbsentValuesEmpty(t *testing.T) {
	item := teamFrom(decode[gitea.Team](t, `{"id":1,"name":"Owners","permission":"owner","organization":null,"units_map":null}`))
	require.Equal(t, Team{ID: 1, Name: "Owners", Permission: "owner", Units: map[string]string{}}, item)
}

func TestTeamUnitLevelAppliesThePermissionToEveryUnit(t *testing.T) {
	team := Team{Permission: "write", Units: map[string]string{"repo.wiki": "none"}}
	require.Equal(t, "write", team.UnitLevel("repo.code"))
	require.Equal(t, "write", team.UnitLevel("repo.wiki"))
	require.Equal(t, "read", team.UnitLevel("repo.ext_wiki"))
	require.Equal(t, "write", Team{Permission: "none", Units: map[string]string{"repo.code": "write"}}.UnitLevel("repo.code"))
	require.Equal(t, "none", Team{Permission: "none"}.UnitLevel("repo.code"))
}

func TestTeamAccessSummarizesUnits(t *testing.T) {
	for _, tc := range []struct {
		permission string
		units      map[string]string
		want       string
	}{
		{"owner", UnitPolicy("write"), "owner"},
		{"admin", nil, "admin"},
		{"none", UnitPolicy("write"), "write"},
		{"none", UnitPolicy("read"), "read"},
		{"none", map[string]string{"repo.code": "write", "repo.issues": "write", "repo.pulls": "write", "repo.wiki": "none", "repo.ext_wiki": "read"}, "write (code, issues, pulls)"},
		{"none", map[string]string{"repo.code": "write", "repo.wiki": "read"}, "custom"},
		{"none", map[string]string{"repo.ext_issues": "read"}, "none"},
		// Gitea grants the team permission on every unit when the team has no unit rows.
		{"write", nil, "write"},
		{"read", map[string]string{}, "read"},
		{"write", map[string]string{"repo.wiki": "read"}, "write"},
		{"read", map[string]string{"repo.code": "write"}, "custom"},
	} {
		require.Equal(t, tc.want, Team{Permission: tc.permission, Units: tc.units}.Access(), "%v", tc.units)
	}
}

func TestTeamFindSearchesTheOrganization(t *testing.T) {
	var queries []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/orgs/nope/teams/search" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		require.Equal(t, "/api/v1/orgs/acme/teams/search", r.URL.Path)
		queries = append(queries, r.URL.Query().Get("q"))
		if r.URL.Query().Get("page") != "1" {
			_, _ = w.Write([]byte(`{"ok":true,"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":[{"id":2,"name":"dev"},{"id":3,"name":"Dev"},{"id":4,"name":"devops"}]}`))
	}))
	service := Teams{Client: client}
	team, err := service.Find(context.Background(), "acme", "devops")
	require.NoError(t, err)
	require.Equal(t, Team{ID: 4, Name: "devops", Organization: "acme", Units: map[string]string{}}, team)
	team, err = service.Find(context.Background(), "acme", "3")
	require.NoError(t, err)
	require.Equal(t, "Dev", team.Name)
	_, err = service.Find(context.Background(), "acme", "DEV")
	require.EqualError(t, err, `team "DEV" is ambiguous in acme (IDs 2, 3); pass an ID`)
	_, err = service.Find(context.Background(), "nope", "dev")
	require.EqualError(t, err, `organization "nope" not found`)
	require.Equal(t, []string{"devops", "devops", "", "", "DEV", "DEV"}, queries)
}

func TestTeamEditSendsThePermissionBeforeTheUnits(t *testing.T) {
	var bodies []map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/teams/5", r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"admin"}`))
			return
		}
		bodies = append(bodies, requestJSON(t, r))
		_, _ = w.Write([]byte(`{"id":5,"name":"devs","permission":"write"}`))
	}))
	permission := "write"
	_, err := (Teams{Client: client}).Edit(context.Background(), 5, TeamOptions{Permission: &permission, Units: map[string]string{"repo.wiki": "read"}, ReplaceUnits: true})
	require.NoError(t, err)
	require.Len(t, bodies, 2)
	require.Equal(t, map[string]any{"name": "devs", "permission": "write"}, bodies[0], "Gitea 28 keeps an admin team admin when one request sets both")
	require.Equal(t, map[string]any{"repo.wiki": "read"}, bodies[1]["units_map"])
}
