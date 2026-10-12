// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestOrganizationEditsPreserveOmittedFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/orgs/team", r.URL.Path)
		body := requestJSON(t, r)
		require.Len(t, body, 2)
		require.Equal(t, "", body["description"])
		require.Equal(t, false, body["repo_admin_change_team_access"])
		_, _ = w.Write([]byte(`{"id":1,"username":"team","visibility":"private"}`))
	}))
	description := ""
	access := false
	service := Organizations{Client: client}
	item, err := service.Edit(context.Background(), "team", OrganizationOptions{Description: &description, RepoAdminChangeTeamAccess: &access})
	require.NoError(t, err)
	require.Equal(t, "team", item.Name)
	require.Equal(t, "private", item.Visibility)
}

func TestOrganizationLookupTreatsMissingOrganizationAsUser(t *testing.T) {
	for name, tc := range map[string]struct {
		status        int
		body          string
		wantID        int64
		wantOrg       bool
		wantStatusErr int
	}{
		"organization":   {status: http.StatusOK, body: `{"id":9,"name":"acme"}`, wantID: 9, wantOrg: true},
		"user namespace": {status: http.StatusNotFound, body: `{"message":"not found"}`},
		"server failure": {status: http.StatusInternalServerError, body: `{}`, wantStatusErr: http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/orgs/acme", r.URL.Path)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			id, isOrg, err := organization(context.Background(), client, "acme")
			if tc.wantStatusErr != 0 {
				var failure *transport.APIError
				require.True(t, errors.As(err, &failure))
				require.Equal(t, tc.wantStatusErr, failure.Status)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantID, id)
			require.Equal(t, tc.wantOrg, isOrg)
		})
	}
}

func TestOrganizationListSelectsScopeAndPage(t *testing.T) {
	for name, tc := range map[string]struct {
		mine     bool
		username string
		path     string
	}{
		"public": {path: "/api/v1/orgs"},
		"mine":   {mine: true, path: "/api/v1/user/orgs"},
		"user":   {username: "alice", path: "/api/v1/users/alice/orgs"},
	} {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tc.path, r.URL.Path)
				require.Equal(t, "2", r.URL.Query().Get("limit"))
				require.Equal(t, "3", r.URL.Query().Get("page"))
				w.Header().Set("X-Total-Count", "7")
				_, _ = w.Write([]byte(`[{"id":1,"username":"legacy"},{"id":2,"name":"acme"}]`))
			}))
			items, err := (Organizations{Client: client}).List(context.Background(), tc.mine, tc.username, 2, 3)
			require.NoError(t, err)
			require.True(t, items.More)
			require.Equal(t, 7, items.Total)
			require.Equal(t, []string{"legacy", "acme"}, []string{items.Items[0].Name, items.Items[1].Name})
		})
	}
}

func TestOrganizationCreateSendsSuppliedPreferences(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/orgs", r.URL.Path)
		body := requestJSON(t, r)
		require.Equal(t, "acme", body["username"])
		require.Equal(t, "limited", body["visibility"])
		require.Equal(t, true, body["repo_admin_change_team_access"])
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5,"name":"acme","visibility":"limited","repo_admin_change_team_access":true}`))
	}))
	name, visibility, access := "acme", "limited", true
	item, err := (Organizations{Client: client}).Create(context.Background(), OrganizationOptions{Name: &name, Visibility: &visibility, RepoAdminChangeTeamAccess: &access})
	require.NoError(t, err)
	require.Equal(t, Organization{ID: 5, Name: "acme", Visibility: "limited", RepoAdminChangeTeamAccess: true}, item)
}

func TestOrganizationMembershipRequests(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	service := Organizations{Client: client}
	member, err := service.IsMember(context.Background(), "acme", "bob")
	require.NoError(t, err)
	require.True(t, member)
	require.NoError(t, service.SetMemberPublic(context.Background(), "acme", "bob", true))
	require.NoError(t, service.RemoveMember(context.Background(), "acme", "bob"))
	require.Equal(t, []string{
		"GET /api/v1/orgs/acme/members/bob", "PUT /api/v1/orgs/acme/public_members/bob", "DELETE /api/v1/orgs/acme/members/bob",
	}, requests)
}

func TestOrganizationMembersCheckEachMembership(t *testing.T) {
	// Gitea lists concealed members among the public ones to administrators; only the check of
	// each membership tells them apart.
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/members", "/api/v1/orgs/acme/public_members":
			w.Header().Set("X-Total-Count", "2")
			_, _ = w.Write([]byte(`[{"id":1,"login":"alice"},{"id":2,"login":"bob","full_name":"Bob"}]`))
		case "/api/v1/orgs/acme/public_members/alice":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/orgs/acme/public_members/bob":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	alice, bob := OrganizationMember{ID: 1, Username: "alice"}, OrganizationMember{ID: 2, Username: "bob", FullName: "Bob"}
	public, concealed := alice, bob
	public.Visibility, concealed.Visibility = MembershipPublic, MembershipConcealed
	for name, tc := range map[string]struct {
		opts    MemberListOptions
		want    []OrganizationMember
		counted bool
	}{
		"names":      {MemberListOptions{}, []OrganizationMember{alice, bob}, true},
		"visibility": {MemberListOptions{Visibility: true}, []OrganizationMember{public, concealed}, true},
		"public":     {MemberListOptions{Public: true}, []OrganizationMember{public}, false},
	} {
		t.Run(name, func(t *testing.T) {
			tc.opts.Limit, tc.opts.Page = 10, 1
			members, err := (Organizations{Client: client}).Members(context.Background(), "acme", tc.opts)
			require.NoError(t, err)
			require.Equal(t, tc.want, members.Items)
			require.Equal(t, tc.counted, members.Counted)
		})
	}
}
