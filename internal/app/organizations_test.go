// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestOrganizationMemberChangesCheckMembershipFirst(t *testing.T) {
	var changes []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme/members/bob":
			w.WriteHeader(http.StatusNotFound)
		case "GET /api/v1/orgs/acme/members/carol", "GET /api/v1/orgs/acme/public_members/carol":
			w.WriteHeader(http.StatusNoContent)
		default:
			changes = append(changes, r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.OutTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "member", "remove", "acme", "bob", "--yes"}))
	require.Equal(t, "! bob is not a member of acme\n", withoutANSI(diagnostics.String()))
	err := Execute(context.Background(), deps, []string{"org", "member", "publicize", "acme", "bob"})
	require.EqualError(t, err, "bob is not a member of acme")
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "member", "public", "acme", "carol"}))
	require.Equal(t, "! carol's membership of acme is already public\n", withoutANSI(diagnostics.String()))
	require.Empty(t, changes)
}

func TestOrganizationDeleteAsksToTypeTheName(t *testing.T) {
	var deleted []string
	repos := "0"
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":4,"name":"acme"}`))
		case "GET /api/v1/orgs/acme/repos":
			w.Header().Set("X-Total-Count", repos)
			_, _ = w.Write([]byte(`[]`))
		case "GET /api/v1/orgs/acme/members", "GET /api/v1/orgs/acme/teams":
			_, _ = w.Write([]byte(`[]`))
		case "DELETE /api/v1/orgs/acme":
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	const prompt = "Delete organization acme and its teams? This cannot be undone. Type acme to confirm:"
	prompter := &scriptedPrompter{inputs: map[string]string{prompt: "yes"}, accept: true}
	deps.Prompter = prompter
	err := Execute(context.Background(), deps, []string{"org", "delete", "acme"})
	require.Equal(t, clierr.Canceled, clierr.ExitCode(err), "%v", err)
	require.Equal(t, []string{prompt}, prompter.asked)
	require.Empty(t, prompter.confirmations)
	prompter.inputs[prompt] = "acme"
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "delete", "acme"}))
	require.Equal(t, []string{"/api/v1/orgs/acme"}, deleted)

	repos = "2"
	err = Execute(context.Background(), deps, []string{"org", "delete", "acme", "--yes"})
	require.EqualError(t, err, "organization acme still owns repositories; transfer or delete them first (see gt repo list acme)")
	require.Len(t, deleted, 1)
}

func TestOrganizationViewShowsDetailsAndCounts(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orgs/acme":
			_, _ = w.Write([]byte(`{"id":4,"name":"acme","full_name":"ACME Corp","description":"Tools","website":"https://acme.example","visibility":"public"}`))
		case "/api/v1/orgs/acme/members", "/api/v1/orgs/acme/teams", "/api/v1/orgs/acme/repos":
			w.Header().Set("X-Total-Count", map[string]string{"members": "2", "teams": "3", "repos": "1"}[r.URL.Path[len("/api/v1/orgs/acme/"):]])
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "view", "acme"}))
	require.Equal(t, "title:\tACME Corp\nname:\tacme\nvisibility:\tpublic\nwebsite:\thttps://acme.example\nlocation:\t\n"+
		"repo admins manage team access:\tno\nmembers:\t2\nteams:\t3\nrepositories:\t1\nurl:\t"+server.URL+"/acme\n--\nTools\n", out.String())
}
