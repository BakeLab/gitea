// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommentConfirmationNamesTheTarget(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/repos/alice/project/issues/3/comments":
			_, _ = w.Write([]byte(`{"id":12,"body":"Prompted"}`))
		case "DELETE /api/v1/repos/alice/project/issues/comments/12":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	prompt := &scriptedPrompter{accept: true, input: "Prompted"}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "create", "3", "-R", "alice/project"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "delete", "12", "-R", "alice/project"}))
	require.Equal(t, []string{"Comment on alice/project#3", "Delete comment 12 on alice/project"}, prompt.confirmations)
}
