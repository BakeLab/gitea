// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"

	"github.com/stretchr/testify/require"
)

func TestAdminEditPreservesAuthenticationAndExplicitFalse(t *testing.T) {
	mutations, reads := 0, 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reads++
			require.Equal(t, "/api/v1/admin/users", r.URL.Path)
			require.Equal(t, "account", r.URL.Query().Get("q"))
			if mutations == 0 {
				_, _ = w.Write([]byte(`[{"id":2,"login":"account-2"},{"id":1,"login":"account","login_name":"external-identity","source_id":7,"is_admin":true}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":1,"login":"account","is_admin":false,"created":"2026-01-02T03:04:05Z"}]`))
			return
		}
		mutations++
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/admin/users/account", r.URL.Path)
		payload := requestJSON(t, r)
		require.Equal(t, "external-identity", payload["login_name"])
		require.EqualValues(t, 7, payload["source_id"])
		require.Equal(t, false, payload["admin"])
		require.Equal(t, "", payload["description"])
		require.Empty(t, payload["password"], "an empty password leaves it unchanged")
		require.Nil(t, payload["active"], "an omitted flag leaves it unchanged")
		w.WriteHeader(http.StatusNoContent)
	}))
	admin := false
	description := ""
	item, err := (AdminUsers{Client: client}).Edit(context.Background(), "account", AdminUserOptions{Admin: &admin, Description: &description})
	require.NoError(t, err)
	require.Equal(t, "account", item.Username)
	require.False(t, item.Admin)
	require.Equal(t, "2026-01-02T03:04:05Z", item.CreatedAt)
	require.Equal(t, 1, mutations)
	require.Equal(t, 2, reads)
}

func TestAdminCreateAppliesRestrictionsAndReloads(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			payload := requestJSON(t, r)
			require.Equal(t, "new-account", payload["username"])
			require.Equal(t, "new-account", payload["login_name"])
			require.Equal(t, "private", payload["visibility"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":4,"login":"new-account","login_name":"new-account"}`))
		case http.MethodPatch:
			payload := requestJSON(t, r)
			require.Equal(t, true, payload["restricted"])
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte(`[{"id":4,"login":"new-account","restricted":true}]`))
		}
	}))
	restricted := true
	email, visibility := "account@example.test", "private"
	item, err := (AdminUsers{Client: client}).Create(context.Background(), "new-account", AdminUserOptions{Email: &email, Password: "local-password", Visibility: &visibility, Restricted: &restricted})
	require.NoError(t, err)
	require.True(t, item.Restricted)
	require.Equal(t, []string{"POST /api/v1/admin/users", "PATCH /api/v1/admin/users/new-account", "GET /api/v1/admin/users"}, requests)
}

func TestAdminCreateWordsARefusedPassword(t *testing.T) {
	for code, want := range map[string]string{
		"PasswordIsRequired": "password is shorter than the instance's minimum length",
		"PasswordComplexity": "password does not meet the instance's complexity rules",
	} {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"` + code + `"}`))
		}))
		email := "account@example.test"
		_, err := (AdminUsers{Client: client}).Create(context.Background(), "new-account", AdminUserOptions{Email: &email, Password: "short"})
		require.EqualError(t, err, want)
		apiErr, ok := errors.AsType[*transport.APIError](err)
		require.True(t, ok)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
	}
}

func TestAdminCreateReportsFollowupFailureWithoutRetrying(t *testing.T) {
	creates, edits := 0, 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			creates++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":4,"login":"new-account","login_name":"new-account"}`))
			return
		}
		edits++
		w.WriteHeader(http.StatusForbidden)
	}))
	admin := true
	email := "account@example.test"
	item, err := (AdminUsers{Client: client}).Create(context.Background(), "new-account", AdminUserOptions{Email: &email, Password: "local-password", Admin: &admin})
	require.ErrorContains(t, err, "new-account created")
	require.NotContains(t, err.Error(), "local-password")
	require.EqualValues(t, 4, item.ID)
	require.Equal(t, 1, creates)
	require.Equal(t, 1, edits)
}

func TestAdminUserFromMapsServerFields(t *testing.T) {
	user := decode[gitea.User](t, `{"id":3,"login":"alice","email":"alice@example.test","full_name":"Alice","description":"Admin",
		"website":"https://alice.test","location":"Earth","html_url":"https://gitea.test/alice","is_admin":true,"active":true,
		"restricted":true,"prohibit_login":true,"visibility":"limited","created":"2026-10-01T12:00:00+02:00"}`)
	require.Equal(t, AdminUser{
		ID: 3, Username: "alice", Email: "alice@example.test", FullName: "Alice", Description: "Admin", Website: "https://alice.test",
		Location: "Earth", URL: "https://gitea.test/alice", Admin: true, Active: true, Restricted: true, ProhibitLogin: true,
		Visibility: "limited", CreatedAt: "2026-10-01T12:00:00+02:00",
	}, adminUserFrom(user))
}

func TestAdminViewFindsOnlyIndividualAccountsByExactName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/admin/users", r.URL.Path, "the public /users/{name} endpoint also answers for organizations and hides roles from other accounts")
		_, _ = w.Write([]byte(`[{"id":2,"login":"acme-bot"}]`))
	}))
	_, err := (AdminUsers{Client: client}).View(context.Background(), "acme")
	require.EqualError(t, err, `user "acme" not found`)
}

func TestAdminListAndSearchSendTheQuery(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/users":
			require.Equal(t, "ali", r.URL.Query().Get("q"))
			require.Equal(t, "2", r.URL.Query().Get("page"))
			w.Header().Set("X-Total-Count", "3")
		case "/api/v1/users/search":
			require.Equal(t, "ali", r.URL.Query().Get("q"))
			require.Equal(t, "5", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`{"ok":true,"data":[{"id":3,"login":"alice"}]}`))
			return
		case "/api/v1/settings/api":
			_, _ = w.Write([]byte(`{"max_response_items":50}`))
			return
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":3,"login":"alice"}]`))
	}))
	service := AdminUsers{Client: client}
	page, err := service.List(context.Background(), "ali", 2, 2)
	require.NoError(t, err)
	require.Equal(t, Page[AdminUser]{Items: []AdminUser{{ID: 3, Username: "alice"}}, Total: 3, Counted: true}, page)
	found, err := service.Search(context.Background(), "ali", 5)
	require.NoError(t, err)
	require.Equal(t, []AdminUser{{ID: 3, Username: "alice"}}, found)
}

func TestAdminDeleteNamesWhatTheUserStillOwns(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		switch r.URL.Path {
		case "/api/v1/admin/users/bob":
			if r.URL.Query().Get("purge") == "true" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"user still has ownership of repositories [uid: 2]"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	service := AdminUsers{Client: client}
	require.EqualError(t, service.Delete(context.Background(), "bob", false), `user "bob" still owns repositories`)
	require.NoError(t, service.Delete(context.Background(), "bob", true))
	require.EqualError(t, service.Delete(context.Background(), "ghost", true), `user "ghost" not found`)
}
