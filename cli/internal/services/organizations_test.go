// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
				require.ErrorAs(t, err, &failure)
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

func TestOrganizationMembershipChecksReportWhatGiteaRefuses(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/members/bob":
			http.Redirect(w, r, "/api/v1/orgs/acme/public_members/bob", http.StatusSeeOther)
		case "/api/v1/orgs/acme/members/bbo":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"user does not exist [uid: 0, name: bbo]"}`))
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"name":"acme"}`))
		case "/api/v1/users/carol":
			_, _ = w.Write([]byte(`{"login":"carol"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := Organizations{Client: client}
	// Gitea answers a missing organization with the same 404 as a missing membership.
	_, err := service.IsMember(context.Background(), "nobody", "carol")
	require.EqualError(t, err, `organization "nobody" not found`)
	_, err = service.IsMember(context.Background(), "acme", "bob")
	require.True(t, IsForbidden(err), "%v", err)
	require.EqualError(t, err, "checking memberships of acme requires membership of acme")
	_, err = service.IsMember(context.Background(), "acme", "bbo")
	require.EqualError(t, err, `user "bbo" not found`)
	_, err = service.IsMember(context.Background(), "acme", "ghost")
	require.EqualError(t, err, `user "ghost" not found`)
	member, err := service.IsMember(context.Background(), "acme", "carol")
	require.NoError(t, err)
	require.False(t, member)
}

func TestOrganizationCreateNamesTheTakenName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"user already exists [name: bob]"}`))
	}))
	name := "bob"
	_, err := (Organizations{Client: client}).Create(context.Background(), OrganizationOptions{Name: &name})
	require.EqualError(t, err, `name "bob" already exists`)
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

// newAdminTestClient returns a client signed in as an administrator, to whom Gitea lists
// concealed members among the public ones.
func newAdminTestClient(t *testing.T, handler http.Handler) *transport.Client {
	t.Helper()
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"id":99,"login":"root","is_admin":true}`))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	client.Token = "token"
	return client
}

func TestOrganizationMembersCheckEachMembership(t *testing.T) {
	// Gitea lists concealed members among the public ones to administrators; only the check of
	// each membership tells them apart.
	client := newAdminTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestOrganizationPublicMembersReadLaterPages(t *testing.T) {
	// The first server page holds only concealed members; a public one follows on page 2.
	client := newAdminTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/acme/public_members" && r.URL.Query().Get("page") == "1":
			var users []string
			for i := range 50 {
				users = append(users, fmt.Sprintf(`{"id":%d,"login":"hidden%d"}`, i+10, i))
			}
			w.Header().Set("Link", `<http://x/api/v1/orgs/acme/public_members?page=2>; rel="next"`)
			_, _ = w.Write([]byte("[" + strings.Join(users, ",") + "]"))
		case r.URL.Path == "/api/v1/orgs/acme/public_members":
			_, _ = w.Write([]byte(`[{"id":1,"login":"alice"}]`))
		case r.URL.Path == "/api/v1/orgs/acme/public_members/alice":
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/api/v1/orgs/acme/public_members/hidden"):
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	members, err := (Organizations{Client: client}).Members(context.Background(), "acme", MemberListOptions{Public: true, Visibility: true, Limit: 1, Page: 1})
	require.NoError(t, err)
	require.Equal(t, []OrganizationMember{{ID: 1, Username: "alice", Visibility: MembershipPublic}}, members.Items)
	require.False(t, members.More)
}

func TestOrganizationWithoutPublicMembersListsNone(t *testing.T) {
	client := newAdminTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme/public_members":
			_, _ = w.Write([]byte(`[{"id":2,"login":"bob"}]`))
		case "/api/v1/orgs/acme/public_members/bob":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	members, err := (Organizations{Client: client}).Members(context.Background(), "acme", MemberListOptions{Public: true, Limit: 10, Page: 1})
	require.NoError(t, err)
	require.NotNil(t, members.Items, "an empty list is [] in JSON, not null")
	require.Empty(t, members.Items)
}

func TestOrganizationMembersReadPublicMembersOnce(t *testing.T) {
	// To anyone but an administrator Gitea lists exactly the public memberships, so one listing
	// tells every member's visibility apart.
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/user":
			_, _ = w.Write([]byte(`{"id":3,"login":"carol"}`))
		case "/api/v1/orgs/acme/members":
			w.Header().Set("X-Total-Count", "3")
			_, _ = w.Write([]byte(`[{"id":1,"login":"alice"},{"id":2,"login":"bob"},{"id":3,"login":"carol"}]`))
		case "/api/v1/orgs/acme/public_members":
			w.Header().Set("X-Total-Count", "2")
			_, _ = w.Write([]byte(`[{"id":1,"login":"alice"},{"id":3,"login":"carol"}]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	client.Token = "token"
	service := Organizations{Client: client}

	members, err := service.Members(context.Background(), "acme", MemberListOptions{Visibility: true, Limit: 10, Page: 1})
	require.NoError(t, err)
	require.Equal(t, []OrganizationMember{
		{ID: 1, Username: "alice", Visibility: MembershipPublic},
		{ID: 2, Username: "bob", Visibility: MembershipConcealed},
		{ID: 3, Username: "carol", Visibility: MembershipPublic},
	}, members.Items)
	require.Equal(t, []string{"GET /api/v1/orgs/acme/members", "GET /api/v1/user", "GET /api/v1/orgs/acme/public_members"}, requests)

	requests = nil
	members, err = service.Members(context.Background(), "acme", MemberListOptions{Public: true, Limit: 10, Page: 1})
	require.NoError(t, err)
	require.Equal(t, []OrganizationMember{
		{ID: 1, Username: "alice", Visibility: MembershipPublic},
		{ID: 3, Username: "carol", Visibility: MembershipPublic},
	}, members.Items)
	require.True(t, members.Counted)
	require.Equal(t, 2, members.Total)
	require.Equal(t, []string{"GET /api/v1/user", "GET /api/v1/orgs/acme/public_members"}, requests)
}

func TestAccountListsNameTheMissingUser(t *testing.T) {
	// Gitea answers an unknown account name with "user redirect does not exist".
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"user redirect does not exist [name: nosuch]"}`))
	}))
	_, err := (Organizations{Client: client}).List(context.Background(), false, "nosuch", 10, 1)
	require.EqualError(t, err, `user "nosuch" not found`)
	_, err = (Repositories{Client: client}).ListPage(context.Background(), RepositoryListOptions{Owner: "nosuch", Limit: 10, Page: 1})
	require.EqualError(t, err, `user "nosuch" not found`)
	_, err = (Repositories{Client: client}).Search(context.Background(), RepositorySearchOptions{Owner: "nosuch", Limit: 10, Page: 1})
	require.EqualError(t, err, `user "nosuch" not found`)
}

func TestOrganizationMembersNameTheMissingOrganization(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	for _, public := range []bool{false, true} {
		_, err := (Organizations{Client: client}).Members(context.Background(), "nope", MemberListOptions{Public: public, Limit: 10, Page: 1})
		require.EqualError(t, err, `organization "nope" not found`)
	}
}

func TestCanGrantTeamAccessFollowsGiteasRules(t *testing.T) {
	for _, tc := range []struct {
		name, user, permissions, org string
		member                       int
		want                         bool
		err                          string
	}{
		{name: "site administrator", user: `{"login":"bob","is_admin":true}`, want: true},
		{name: "owner", user: `{"login":"bob"}`, permissions: `{"is_owner":true}`, want: true},
		{name: "team access fixed", user: `{"login":"bob"}`, permissions: `{}`, org: `{"repo_admin_change_team_access":false}`},
		{name: "team member", user: `{"login":"bob"}`, permissions: `{}`, org: `{"repo_admin_change_team_access":true}`, member: http.StatusOK, want: true},
		{name: "not a member", user: `{"login":"bob"}`, permissions: `{}`, org: `{"repo_admin_change_team_access":true}`, member: http.StatusForbidden},
		{name: "missing organization", user: `{"login":"bob"}`, err: `organization "acme" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := map[string]string{"/api/v1/user": tc.user, "/api/v1/users/bob/orgs/acme/permissions": tc.permissions, "/api/v1/orgs/acme": tc.org}[r.URL.Path]
				switch {
				case r.URL.Path == "/api/v1/teams/4/members/bob" && tc.member != 0:
					w.WriteHeader(tc.member)
					_, _ = w.Write([]byte(`{"login":"bob"}`))
				case body == "":
					w.WriteHeader(http.StatusNotFound)
				default:
					_, _ = w.Write([]byte(body))
				}
			}))
			allowed, err := (Organizations{Client: client}).CanGrantTeamAccess(context.Background(), "acme", 4)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, allowed)
		})
	}
}

func TestOrganizationRepositoryCountAndDelete(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/repos":
			require.Equal(t, "1", r.URL.Query().Get("limit"))
			w.Header().Set("X-Total-Count", "12")
			_, _ = w.Write([]byte(`[{"id":1}]`))
		case "DELETE /api/v1/orgs/acme":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	service := Organizations{Client: client}
	count, counted, err := service.RepositoryCount(context.Background(), "acme")
	require.NoError(t, err)
	require.True(t, counted)
	require.Equal(t, 12, count)
	_, counted, err = service.RepositoryCount(context.Background(), "hidden")
	require.NoError(t, err)
	require.False(t, counted, "a count the account cannot read is unknown")
	require.NoError(t, service.Delete(context.Background(), "acme"))
}

func TestOwnerAndSudoUserLookupsNameWhatIsMissing(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Sudo"), "the Sudo user is looked up as the account itself")
		switch r.URL.Path {
		case "/api/v1/users/alice":
			_, _ = w.Write([]byte(`{"login":"alice"}`))
		case "/api/v1/users/private":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	require.NoError(t, OwnerExists(context.Background(), client, "alice"))
	require.NoError(t, OwnerExists(context.Background(), client, "private"))
	require.EqualError(t, OwnerExists(context.Background(), client, "ghost"), `user or organization "ghost" not found`)
	require.NoError(t, MissingSudoUser(context.Background(), client))
	client.Sudo = "alice"
	require.NoError(t, MissingSudoUser(context.Background(), client))
	client.Sudo = "ghost"
	require.EqualError(t, MissingSudoUser(context.Background(), client), `--sudo user "ghost" not found`)
}
