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

func TestMilestoneNameSelectionResolvesAllPagesBeforeEditing(t *testing.T) {
	duplicate := false
	edits := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path == "/api/v1/repos/alice/project/milestones/9" {
				_, _ = w.Write([]byte(`{"id":9,"title":"Release one"}`))
				return
			}
			require.Equal(t, "/api/v1/repos/alice/project/milestones", r.URL.Path)
			require.Equal(t, "all", r.URL.Query().Get("state"))
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write([]byte(`[{"id":9,"title":"Release one"}]`))
			case "2":
				if duplicate {
					_, _ = w.Write([]byte(`[{"id":10,"title":"Release one"}]`))
				} else {
					_, _ = w.Write([]byte(`[]`))
				}
			default:
				_, _ = w.Write([]byte(`[]`))
			}
			return
		}
		edits++
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/milestones/9", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":9,"title":"Release one","state":"closed"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"milestone", "close", "Release one", "--by-name", "-R", "alice/project", "--json=id,state"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `{"id":9,"state":"closed"}`, out.String())
	duplicate = true
	out.Reset()
	require.ErrorContains(t, Execute(context.Background(), deps, args), "ambiguous")
	require.Empty(t, out.String())
	require.Equal(t, 1, edits)
}

func TestMilestoneDeleteReturnsJSONSnapshotOnlyAfterSuccess(t *testing.T) {
	calls := []string{}
	fail := false
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/milestones/9", r.URL.Path)
		calls = append(calls, r.Method)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":9,"title":"Release one","state":"open"}`))
			return
		}
		require.Equal(t, http.MethodDelete, r.Method)
		if fail {
			w.WriteHeader(403)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	args := []string{"milestone", "delete", "9", "-R", "alice/project", "--yes", "--json=id,title"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.JSONEq(t, `{"id":9,"title":"Release one"}`, out.String())
	require.Equal(t, []string{"GET", "DELETE"}, calls)
	fail = true
	out.Reset()
	require.Error(t, Execute(context.Background(), deps, args))
	require.Empty(t, out.String())
}

func TestMilestoneIssueCommandsSelectByName(t *testing.T) {
	var calls []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/milestones":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":9,"title":"Release one"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues" && r.Method == http.MethodGet:
			require.Equal(t, "9", r.URL.Query().Get("milestones"))
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v1/repos/alice/project/issues/3" && r.Method == http.MethodPatch:
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.EqualValues(t, 9, payload["milestone"])
			_, _ = w.Write([]byte(`{"number":3,"milestone":{"id":9}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "issue", "list", "Release one", "--by-name", "-R", "alice/project", "--json"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"milestone", "issue", "add", "Release one", "3", "--by-name", "-R", "alice/project", "--json=number"}))
	require.NotContains(t, calls, "GET /api/v1/repos/alice/project/milestones/9")
	err := Execute(context.Background(), deps, []string{"milestone", "issue", "remove", " ", "3", "--by-name", "-R", "alice/project"})
	require.ErrorContains(t, err, "milestone name cannot be empty")
}
