// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIssueCreationMetadataAndReferenceClearing(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/owner/repo/milestones" {
			writeFirstPage(w, r, `[{"id":4,"title":"Release"}]`)
			return
		}
		calls++
		if calls == 1 {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/issues", r.URL.Path)
			requirePayload(t, `{"title":"Title","ref":"release/v1","assignees":["alice","bob"],"milestone":4}`, r.Body)
			_, _ = w.Write([]byte(`{"number":1,"ref":"release/v1","assignees":[{"login":"alice","email":"excluded@example.test"},{"login":"bob"}],"labels":[{"id":2,"name":"bug","color":"ff0000","description":"Defect","exclusive":true}],"milestone":{"id":4,"title":"Release","description":"excluded"}}`))
			return
		}
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/issues/1", r.URL.Path)
		requirePayload(t, `{"ref":""}`, r.Body)
		_, _ = w.Write([]byte(`{"number":1,"ref":"","assignees":null,"milestone":null}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	fields := "--json=number,ref,assignees,milestone,labels"
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "create", "-R", "owner/repo", "--title", "Title", "--ref", "release/v1", "--assignee", "alice,bob", "--milestone", "4", fields, "--no-input"}))
	require.JSONEq(t, `{"number":1,"ref":"release/v1","assignees":["alice","bob"],"milestone":{"id":4,"title":"Release"},"labels":[{"id":2,"name":"bug","color":"ff0000","description":"Defect","exclusive":true,"is_archived":false}]}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "edit", "1", "-R", "owner/repo", "--ref=", fields}))
	require.JSONEq(t, `{"number":1,"ref":"","assignees":[],"milestone":null,"labels":[]}`, out.String())
	require.Equal(t, 2, calls)
	require.Empty(t, diagnostics.String())
}

func TestNameFlagsAreTrimmed(t *testing.T) {
	var routes, payloads []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/orgs/owner":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/v1/repos/owner/repo/labels" && r.URL.Query().Get("page") == "1":
			_, _ = w.Write([]byte(`[{"id":2,"name":"bug"}]`))
		case r.URL.Path == "/api/v1/repos/owner/repo/labels":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"number":1,"assignees":[{"login":"carol"}]}`))
		default:
			payload, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			routes, payloads = append(routes, r.Method+" "+r.URL.Path), append(payloads, string(payload))
			if strings.HasSuffix(r.URL.Path, "/requested_reviewers") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"number":1}`))
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	for _, args := range [][]string{
		{"issue", "create", "--title", "Title", "--assignee", " alice, bob ", "--label", " bug", "--no-input"},
		{"issue", "edit", "1", "--add-assignee", " alice"},
		{"pr", "review", "request", "1", "--reviewer", " alice"},
		{"pr", "edit", "1", "--add-reviewer", "bob "},
		{"pr", "create", "--title", "Title", "--head", "feature", "--base", "main", "--reviewer", " dave", "--no-input"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "owner/repo", "--json=number")), args)
	}
	require.Equal(t, []string{
		"POST /api/v1/repos/owner/repo/issues",
		"PATCH /api/v1/repos/owner/repo/issues/1",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
		"POST /api/v1/repos/owner/repo/pulls",
		"POST /api/v1/repos/owner/repo/pulls/1/requested_reviewers",
	}, routes)
	for i, want := range []string{
		`{"title":"Title","assignees":["alice","bob"],"labels":[2]}`,
		`{"assignees":["carol","alice"]}`,
		`{"reviewers":["alice"]}`,
		`{"reviewers":["bob"]}`,
		`{"title":"Title","head":"feature","base":"main","allow_maintainer_edit":true}`,
		`{"reviewers":["dave"]}`,
	} {
		requirePayload(t, want, strings.NewReader(payloads[i]))
	}
	require.Empty(t, diagnostics.String())
}
