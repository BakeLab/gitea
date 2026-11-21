// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCommandsReachTheirEndpoints(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "bob.pub")
	authorized, _ := testPublicKey(t, "bob@laptop")
	require.NoError(t, os.WriteFile(keyFile, []byte(authorized+"\n"), 0o600))
	var requests []string
	bodies := map[string]string{}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			_, _ = w.Write([]byte(`{"version":"1.24.0"}`))
			return
		}
		key := r.Method + " " + r.URL.RequestURI()
		requests = append(requests, key)
		body, _ := io.ReadAll(r.Body)
		bodies[key] = string(body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/users":
			_, _ = w.Write([]byte(`[{"id":2,"login":"robert"}]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/admin/users/bob/keys" || r.URL.Path == "/api/v1/admin/users/bob/orgs":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"title":"bob@laptop","username":"acme"}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	deps, stdout, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"admin", "user", "rename", "bob", "robert", "--json", "username"},
		{"admin", "user", "key", "create", "bob", keyFile},
		{"admin", "user", "key", "delete", "bob", "7", "--yes"},
		{"admin", "user", "badge", "list", "bob"},
		{"admin", "user", "badge", "add", "bob", "one", "two"},
		{"admin", "user", "badge", "delete", "bob", "one"},
		{"admin", "org", "list"},
		{"admin", "org", "create", "acme", "--owner", "bob"},
		{"admin", "cron", "list"},
		{"admin", "cron", "run", "update_mirrors"},
		{"admin", "email", "list"},
		{"admin", "email", "list", "--query", "example"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "--no-input")), args)
	}
	require.Equal(t, []string{
		"POST /api/v1/admin/users/bob/rename",
		"GET /api/v1/admin/users?limit=50&page=1&q=robert",
		"POST /api/v1/admin/users/bob/keys",
		"DELETE /api/v1/admin/users/bob/keys/7",
		"GET /api/v1/admin/users/bob/badges",
		"POST /api/v1/admin/users/bob/badges",
		"DELETE /api/v1/admin/users/bob/badges",
		"GET /api/v1/admin/orgs?limit=30&page=1",
		"POST /api/v1/admin/users/bob/orgs",
		"GET /api/v1/admin/cron?limit=30&page=1",
		"POST /api/v1/admin/cron/update_mirrors",
		"GET /api/v1/admin/emails?limit=30&page=1",
		"GET /api/v1/admin/emails/search?limit=30&page=1&q=example",
	}, requests)
	require.JSONEq(t, `{"new_username":"robert"}`, bodies["POST /api/v1/admin/users/bob/rename"])
	require.JSONEq(t, `{"title":"bob@laptop","key":"`+authorized+`","read_only":false}`, bodies["POST /api/v1/admin/users/bob/keys"])
	require.JSONEq(t, `{"badge_slugs":["one","two"]}`, bodies["POST /api/v1/admin/users/bob/badges"])
	require.Contains(t, stdout.String(), `{"username":"robert"}`)
}

func TestAdminUserKeyCreateHasNoReadOnlyFlag(t *testing.T) {
	deps, _, _ := testDeps(t)
	create, _, err := NewRoot(deps).Find([]string{"admin", "user", "key", "create"})
	require.NoError(t, err)
	require.Nil(t, create.Flags().Lookup("read-only"), "Gitea stores user keys with write access whatever the request says")
}
