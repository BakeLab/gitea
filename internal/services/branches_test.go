// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBranchRenameEscapesNames(t *testing.T) {
	mutations := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		assert.Equal(t, http.MethodPatch, r.Method)
		assert.Equal(t, "/api/v1/repos/owner/repo/branches/feature/a%23b", r.URL.EscapedPath())
		var payload map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, map[string]string{"name": "feature/renamed"}, payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	err := (Branches{Client: client}).Rename(context.Background(), "owner/repo", "feature/a#b", "feature/renamed")
	require.NoError(t, err)
	require.Equal(t, 1, mutations)
}

func TestBranchListMapsServerFields(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/owner/repo/branches", r.URL.Path)
		assert.Equal(t, "20", r.URL.Query().Get("limit"))
		assert.Equal(t, "3", r.URL.Query().Get("page"))
		_, _ = w.Write([]byte(`[{"name":"main","commit":{"id":"abc","message":"Fix\n\nbody","timestamp":"2026-10-01T12:00:00Z"},"protected":true,"required_approvals":2,"user_can_push":true,"user_can_merge":true},{"name":"dev","commit":{"id":"def","message":"Wip","timestamp":"2026-10-01T14:30:00+02:00"}},{"name":"orphan","commit":null}]`))
	}))
	items, err := (Branches{Client: client}).ListPage(context.Background(), "owner/repo", 20, 3)
	require.NoError(t, err)
	require.Equal(t, []Branch{
		{Name: "main", SHA: "abc", Message: "Fix\n\nbody", UpdatedAt: "2026-10-01T12:00:00Z", Protected: true, RequiredApprovals: 2, CanPush: true, CanMerge: true},
		{Name: "dev", SHA: "def", Message: "Wip", UpdatedAt: "2026-10-01T12:30:00Z"}, // in UTC, whatever the committer's zone
		{Name: "orphan"},
	}, items.Items)
}

func TestBranchCreateAndProtectPayloads(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := requestJSON(t, r)
		switch r.URL.Path {
		case "/api/v1/repos/owner/repo/branches":
			assert.Equal(t, "feature", payload["new_branch_name"])
			assert.Empty(t, payload["old_branch_name"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"feature","commit":{"id":"abc"}}`))
		case "/api/v1/repos/owner/repo/branch_protections":
			assert.Equal(t, "main", payload["rule_name"])
			assert.Equal(t, false, payload["enable_push"])
			assert.EqualValues(t, 1, payload["required_approvals"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"rule_name":"main"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	service := Branches{Client: client}
	item, err := service.Create(context.Background(), "owner/repo", "feature", "")
	require.NoError(t, err)
	require.Equal(t, Branch{Name: "feature", SHA: "abc"}, item)
	created, err := service.Protect(context.Background(), "owner/repo", Protection{Rule: "main", RequiredApprovals: 1})
	require.NoError(t, err)
	require.True(t, created)
}

func TestBranchCreateFromATagOrCommitSendsOldRefName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost+" /api/v1/repos/owner/repo/branches", r.Method+" "+r.URL.Path)
		assert.Equal(t, map[string]any{"new_branch_name": "hotfix", "old_ref_name": "v1.0.0"}, requestJSON(t, r))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"hotfix","commit":{"id":"abc"}}`))
	}))
	item, err := (Branches{Client: client}).Create(context.Background(), "owner/repo", "hotfix", "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, Branch{Name: "hotfix", SHA: "abc"}, item)
}

func TestEditProtectionChangesOnlyTheNamedSettings(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "PATCH /api/v1/repos/owner/repo/branch_protections/release%2F%2A", r.Method+" "+r.URL.EscapedPath())
		payload := requestJSON(t, r)
		for key, value := range payload {
			switch key {
			case "status_check_contexts", "enable_status_check", "enable_push", "enable_push_whitelist", "push_whitelist_usernames", "push_whitelist_teams":
			default:
				require.Nil(t, value, key)
			}
		}
		require.Equal(t, []any{"ci"}, payload["status_check_contexts"])
		require.Equal(t, true, payload["enable_status_check"])
		require.Equal(t, []any{"alice", "bob"}, payload["push_whitelist_usernames"])
		require.Equal(t, []any{}, payload["push_whitelist_teams"], "an empty list replaces the teams; null would keep them")
		// Gitea leaves out users that cannot write to the repository.
		_, _ = w.Write([]byte(`{"rule_name":"release/*","push_whitelist_usernames":["alice"]}`))
	}))
	err := (Branches{Client: client}).EditProtection(context.Background(), "owner/repo",
		Protection{Rule: "release/*", RequiredApprovals: 5, PushUsers: []string{"alice", "bob"}, StatusChecks: []string{"ci"}},
		ProtectionChanges{Pushers: true, StatusChecks: true})
	require.EqualError(t, err, "the rule was updated without bob among those who may push, as only users and teams with write access to owner/repo can push; "+
		"to retry, grant them write access and protect again with the same --allow-push")
}

func TestEditProtectionReplacesThePushersAndTellsWhoCanStillPush(t *testing.T) {
	saved := `{"rule_name":"main","push_whitelist_usernames":[],"push_whitelist_teams":[]}`
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := requestJSON(t, r)
		require.Equal(t, []any{}, payload["push_whitelist_usernames"], "users left out of --allow-push lose push access")
		require.Equal(t, []any{"devs"}, payload["push_whitelist_teams"])
		_, _ = w.Write([]byte(saved))
	}))
	edit := func() error {
		return (Branches{Client: client}).EditProtection(context.Background(), "acme/repo", Protection{Rule: "main", PushTeams: []string{"devs"}}, ProtectionChanges{Pushers: true})
	}
	err := edit()
	require.ErrorContains(t, err, "nobody can push to main now")
	// Who can push is read from the saved rule, not from the request.
	saved = `{"rule_name":"main","push_whitelist_usernames":["alice"],"push_whitelist_teams":[]}`
	err = edit()
	require.ErrorContains(t, err, "the rule was updated without team acme/devs")
	require.NotContains(t, err.Error(), "nobody can push")
}

func TestBranchProtectionIsIdempotent(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/owner/repo/branch_protections":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Branch protection already exist"}`))
		case "DELETE /api/v1/repos/owner/repo/branch_protections/main":
			w.WriteHeader(http.StatusNotFound)
		case "GET /api/v1/repos/owner/repo":
			_, _ = w.Write([]byte(`{"full_name":"owner/repo"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	service := Branches{Client: client}
	created, err := service.Protect(context.Background(), "owner/repo", Protection{Rule: "main"})
	require.NoError(t, err)
	require.False(t, created)
	removed, err := service.Unprotect(context.Background(), "owner/repo", "main")
	require.NoError(t, err)
	require.False(t, removed)
}

func TestBranchProtectNamesUnknownPushers(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		switch {
		case body["push_whitelist_teams"] != nil:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"team does not exist [org_id 14, team_id 0, name: nosuchteam]"}`))
		default:
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"user does not exist [uid: 0, name: nosuchuser]"}`))
		}
	}))
	service := Branches{Client: client}
	ctx := context.Background()
	_, err := service.Protect(ctx, "acme/project", Protection{Rule: "main", PushUsers: []string{"nosuchuser"}})
	require.EqualError(t, err, `user "nosuchuser" not found`)
	_, err = service.Protect(ctx, "acme/project", Protection{Rule: "main", PushTeams: []string{"nosuchteam"}})
	require.EqualError(t, err, `team "nosuchteam" not found`)
}

func TestBranchCreateNamesTheProtectionThatRefusesIt(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"PushRejected Error: exit status 1 - remote: error: Not allowed to push to protected branch rel/one To /data/acme/project.git"}`))
	}))
	for _, source := range []string{"", "main"} {
		_, err := (Branches{Client: client}).Create(context.Background(), "acme/project", "rel/one", source)
		require.EqualError(t, err, "a protection rule of acme/project refuses to create branch rel/one", source)
	}
}
