// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryCommandsAcceptPositionalRepository(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			requirePayload(t, `{"archived":true}`, r.Body)
			_, _ = w.Write([]byte(`{"full_name":"bob/tool","archived":true}`))
			return
		}
		_, _ = w.Write([]byte(`{"full_name":"bob/tool"}`))
	}))
	defer server.Close()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"view slug", []string{"repo", "view", "bob/tool"}, "GET /api/v1/repos/bob/tool"},
		{"view URL", []string{"repo", "view", server.URL + "/bob/tool"}, "GET /api/v1/repos/bob/tool"},
		{"archive", []string{"repo", "archive", "bob/tool", "--yes"}, "PATCH /api/v1/repos/bob/tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests = nil
			deps, out, _ := testDeps(t)
			deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
			require.NoError(t, Execute(context.Background(), deps, append(test.args, "--no-input", "--json=full_name")))
			require.Equal(t, test.want, requests[len(requests)-1])
			require.JSONEq(t, `{"full_name":"bob/tool"}`, out.String())
		})
	}
}

func TestCommandsWithoutRepositoryRejectRepoFlag(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"repo", "list", "-R", "acme/service"}, "--repo is not used by repo list; did you mean: gt repo list acme"},
		{[]string{"repo", "create", "foo", "-R", "alice/project", "--yes"}, "--repo is not used by repo create; did you mean: gt repo create alice/foo"},
		{[]string{"repo", "migrate", "https://example.com/x.git", "--name", "x", "--repo", "acme/x"}, "--repo is not used by repo migrate; did you mean: gt repo migrate URL --owner acme"},
		{[]string{"search", "repo", "gt", "-R", "acme/x"}, "--repo is not used by search repo; did you mean: gt search repo --owner acme"},
	} {
		t.Run(strings.Join(test.args[:2], " "), func(t *testing.T) {
			requireUsageBeforeRequests(t, test.want, test.args...)
		})
	}
}

func TestRepositoryOwnerArguments(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":3,"name":"acme"}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{"full_name":"acme/tool"}`))
		}
	}))
	defer server.Close()
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"repo", "list", "acme"}, "GET /api/v1/orgs/acme/repos"},
		{[]string{"repo", "create", "acme/tool"}, "POST /api/v1/orgs/acme/repos"},
		{[]string{"repo", "create", "alice/tool"}, "POST /api/v1/user/repos"},
	} {
		requests = nil
		deps, _, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, append(test.args, "--no-input", "--json")))
		require.Equal(t, test.want, requests[len(requests)-1], test.args)
	}
}
