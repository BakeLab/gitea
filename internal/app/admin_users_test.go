// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminPromptAndFlagsReachEquivalentPayloads(t *testing.T) {
	var payloads []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"id":1,"login":"account"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	prompt := &scriptedPrompter{input: "account@example.test", secret: " preserved password ", accept: true}
	deps.Prompter = prompt
	deps.Streams.InTTY = true
	deps.Streams.ErrTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "create", "account", "--full-name", "Test Account"}))
	require.Equal(t, []string{"Email address"}, prompt.asked)
	require.Len(t, prompt.secrets, 2)
	require.Empty(t, prompt.confirmations)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "create", "account", "--full-name", "Test Account", "--email", "account@example.test", "--password", " preserved password ", "--no-input"}))
	require.Equal(t, payloads[0], payloads[1])
	require.Len(t, prompt.asked, 1)
	require.Len(t, prompt.secrets, 2)
	require.NotContains(t, out.String(), "password")
}

func TestAdminUserEditSetsMustChangePasswordOnlyWhenPassed(t *testing.T) {
	var edits []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			edits = append(edits, payload)
		}
		_, _ = w.Write([]byte(`{"id":2,"login":"bob"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	for _, flags := range [][]string{{"--password", "new password"}, {"--password", "new password", "--must-change-password"}, {"--must-change-password=false"}} {
		require.NoError(t, Execute(context.Background(), deps, append([]string{"admin", "user", "edit", "bob", "--no-input"}, flags...)))
	}
	require.Len(t, edits, 3)
	require.Equal(t, "new password", edits[0]["password"])
	require.Nil(t, edits[0]["must_change_password"])
	require.Equal(t, true, edits[1]["must_change_password"])
	require.Equal(t, false, edits[2]["must_change_password"])
}

func TestAdminUserListShowsRolesAndStatuses(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/admin/users", r.URL.Path)
		require.Equal(t, "li", r.URL.Query().Get("q"))
		_, _ = w.Write([]byte(`[
			{"id":1,"login":"alice","full_name":"Alice","email":"alice@example.test","is_admin":true,"active":true,"created":"2026-10-01T12:00:00Z"},
			{"id":2,"login":"elisa","email":"elisa@example.test","restricted":true,"active":true,"created":"2026-10-02T12:00:00Z"},
			{"id":3,"login":"lisbeth","email":"lisbeth@example.test","prohibit_login":true,"active":true,"created":"2026-10-03T12:00:00Z"},
			{"id":4,"login":"lin","email":"lin@example.test","active":false,"created":"2026-10-04T12:00:00Z"}]`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "list", "--query", "li"}))
	require.Equal(t, "1\talice\tAlice\talice@example.test\tadmin\tactive\t2026-10-01T12:00:00Z\n"+
		"2\telisa\t\telisa@example.test\trestricted\tactive\t2026-10-02T12:00:00Z\n"+
		"3\tlisbeth\t\tlisbeth@example.test\tuser\tlogin prohibited\t2026-10-03T12:00:00Z\n"+
		"4\tlin\t\tlin@example.test\tuser\tinactive\t2026-10-04T12:00:00Z\n", out.String())
}

func TestAdminUserDeleteOffersPurgeForOwners(t *testing.T) {
	var deletes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			require.Equal(t, "/api/v1/users/bob", r.URL.Path)
			_, _ = w.Write([]byte(`{"id":2,"login":"bob"}`))
		case http.MethodDelete:
			deletes = append(deletes, r.URL.RequestURI())
			if r.URL.Query().Get("purge") != "true" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"user still has ownership of repositories [uid: 2]"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	err := Execute(context.Background(), deps, []string{"admin", "user", "delete", "bob", "--yes"})
	require.EqualError(t, err, `user "bob" still owns repositories; transfer or delete them, or pass --purge to delete them with the user`)

	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	const typed = "Delete user bob with their repositories, packages and organization memberships? This cannot be undone. Type bob to confirm:"
	prompt := &scriptedPrompter{inputs: map[string]string{typed: "bob"}}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"admin", "user", "delete", "bob", "--purge"}))
	require.Equal(t, []string{typed}, prompt.asked)
	require.Equal(t, []string{"/api/v1/admin/users/bob", "/api/v1/admin/users/bob?purge=true"}, deletes)
}
