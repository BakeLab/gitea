// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
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
