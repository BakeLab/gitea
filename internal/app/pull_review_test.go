// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/tea/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestReviewReplyPostsBodyFromStdin(t *testing.T) {
	mutations := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutations++
		require.Equal(t, "token token", r.Header.Get("Authorization"))
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/installation/api/v1/repos/owner/repo/pulls/1/comments/7/replies", r.URL.Path)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]string{"body": "Reply from stdin"}, body)
		_, _ = w.Write([]byte(`{"id":9,"body":"Reply from stdin","user":{"login":"alice"},"pull_request_review_id":3}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL+"/installation")
	deps.Streams.In = strings.NewReader("Reply from stdin")
	err := Execute(context.Background(), deps, []string{"pr", "review", "reply", "1", "7", "-R", "owner/repo", "--body-file=-", "--json=id,review_id,author"})
	require.NoError(t, err)
	require.Equal(t, 1, mutations)
	require.JSONEq(t, `{"id":9,"review_id":3,"author":"alice"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestReviewPromptAndFlagsReachIdenticalOptions(t *testing.T) {
	var payloads []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/pulls/1/reviews", r.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"id":7,"state":"REQUEST_CHANGES","user":{"login":"alice"},"html_url":"https://example.test/review/7"}`))
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	prompt := &scriptedPrompter{input: "Requested changes", accept: true}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	args := []string{"pr", "review", "1", "-R", "owner/repo", "--event", "REQUEST_CHANGES"}
	require.NoError(t, Execute(context.Background(), deps, args))
	require.Len(t, prompt.asked, 1)
	require.Len(t, prompt.confirmations, 1)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(args, "--body", "Requested changes", "--json=id,author,url")))
	require.Len(t, prompt.asked, 1)
	require.Len(t, prompt.confirmations, 1)
	require.Equal(t, payloads[0], payloads[1])
	require.JSONEq(t, `{"id":7,"author":"alice","url":"https://example.test/review/7"}`, out.String())
	require.Empty(t, diagnostics.String())
}

func TestReviewCommentsFileValidatedBeforeRequests(t *testing.T) {
	invalidPosition := "each inline comment requires path, body, and exactly one positive old_position or new_position"
	for _, tc := range []struct{ content, want string }{
		{`null`, "inline comments must be a JSON array"},
		{`[{}]`, invalidPosition},
		{`[{"path":"x","body":"b","new_position":1,"old_position":1}]`, invalidPosition},
		{`[] []`, "inline comments file must contain exactly one JSON array"},
		{`[{"unexpected":true}]`, `unknown field "unexpected"`},
	} {
		t.Run(tc.content, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "comments.json")
			require.NoError(t, os.WriteFile(file, []byte(tc.content), 0o600))
			requireUsageBeforeRequests(t, tc.want, "pr", "review", "1", "--comments-file", file, "--body", "Supplied")
		})
	}
}

func TestReviewDeleteAndDismissRequireYesWithoutInput(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("mutation before confirmation") }))
	defer server.Close()
	for _, verb := range []string{"delete", "dismiss"} {
		deps, _, _ := serverDeps(t, server.URL)
		args := []string{"pr", "review", verb, "1", "2", "-R", "owner/repo", "--no-input"}
		if verb == "dismiss" {
			args = append(args, "--body", "Reason")
		}
		err := Execute(context.Background(), deps, args)
		require.Error(t, err)
		require.Equal(t, clierr.Usage, clierr.ExitCode(err))
		require.ErrorContains(t, err, "--yes")
	}
}
