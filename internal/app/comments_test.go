// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommentConfirmationNamesTheTarget(t *testing.T) {
	var created []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"Crash"}`))
		case "POST /api/v1/repos/alice/project/issues/3/comments":
			created = append(created, payloadField(t, r, "body"))
			_, _ = w.Write([]byte(`{"id":12,"body":"Prompted","html_url":"https://example.org/alice/project/issues/3#issuecomment-12"}`))
		case "GET /api/v1/repos/alice/project/issues/comments/12":
			_, _ = w.Write([]byte(`{"id":12,"body":"Prompted","issue_url":"https://example.org/alice/project/issues/3"}`))
		case "DELETE /api/v1/repos/alice/project/issues/comments/12":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	prompt := &scriptedPrompter{accept: true, input: "Prompted"}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY, deps.Streams.OutTTY = true, true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "3", "-R", "alice/project"}))
	require.Equal(t, []string{"Prompted"}, created)
	require.Equal(t, []string{commentTitle}, prompt.asked)
	require.Equal(t, "https://example.org/alice/project/issues/3#issuecomment-12\n", out.String())
	require.Equal(t, "✓ Commented on alice/project#3 (Crash)\n", diagnostics.String())
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "comment", "delete", "12", "-R", "alice/project"}))
	require.Equal(t, []string{"Delete comment 12 on alice/project#3?"}, prompt.confirmations)
	require.Equal(t, "✓ Deleted comment 12 on alice/project#3\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"issue", "comment", "delete", "12", "-R", "alice/project", "--no-input"})
	require.EqualError(t, err, "confirmation required to delete comment 12 on alice/project#3; rerun with --yes")
}

func TestPullCommentsAndAttachmentsRequirePullRequests(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/1":
			_, _ = w.Write([]byte(`{"number":1,"title":"An issue"}`))
		case "GET /api/v1/repos/alice/project/issues/13":
			_, _ = w.Write([]byte(`{"number":13,"title":"A pull request","pull_request":{"merged":false}}`))
		case "GET /api/v1/repos/alice/project/issues/comments/5":
			_, _ = w.Write([]byte(`{"id":5,"body":"on an issue","issue_url":"http://x/alice/project/issues/1"}`))
		case "GET /api/v1/repos/alice/project/issues/comments/6":
			_, _ = w.Write([]byte(`{"id":6,"body":"on a pull request","pull_request_url":"http://x/alice/project/pulls/13"}`))
		case "GET /api/v1/repos/alice/project/issues/13/comments", "GET /api/v1/repos/alice/project/issues/13/assets":
			_, _ = w.Write([]byte(`[]`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":7,"body":"x"}`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "comment", "create", "1", "--body", "x"}, "#1 in alice/project is an issue, not a pull request; use gt issue comment create 1"},
		{[]string{"pr", "comment", "list", "1"}, "#1 in alice/project is an issue"},
		{[]string{"pr", "comment", "edit", "5", "--body", "x"}, "comment 5 in alice/project is on an issue, not a pull request; use gt issue comment edit 5"},
		{[]string{"pr", "comment", "delete", "5", "--yes"}, "comment 5 in alice/project is on an issue"},
		{[]string{"pr", "attachment", "list", "1"}, "#1 in alice/project is an issue, not a pull request; use gt issue attachment list 1"},
		{[]string{"pr", "attachment", "delete", "1", "2", "--yes"}, "#1 in alice/project is an issue"},
	} {
		err := Execute(context.Background(), deps, append(test.args, "-R", "alice/project"))
		require.ErrorContains(t, err, test.want)
	}
	require.Empty(t, changed)
	require.Empty(t, out.String())
	for _, args := range [][]string{
		{"pr", "comment", "create", "13", "--body", "x"},
		{"pr", "comment", "edit", "6", "--body", "x"},
		{"pr", "comment", "list", "13"},
		{"pr", "attachment", "list", "13"},
		{"issue", "comment", "create", "1", "--body", "x"},
	} {
		require.NoError(t, Execute(context.Background(), deps, append(args, "-R", "alice/project", "--json")))
	}
	require.Equal(t, []string{
		"POST /api/v1/repos/alice/project/issues/13/comments",
		"PATCH /api/v1/repos/alice/project/issues/comments/6",
		"POST /api/v1/repos/alice/project/issues/1/comments",
	}, changed)
}

func TestCommentGroupCommentsLikeGh(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/issues/1":
			_, _ = w.Write([]byte(`{"number":1,"title":"Crash"}`))
		case "GET /api/v1/repos/alice/project/issues/13":
			_, _ = w.Write([]byte(`{"number":13,"title":"Fix","pull_request":{}}`))
		case "GET /api/v1/repos/alice/project/issues/1/comments":
			_, _ = w.Write([]byte(`[{"id":79,"body":"First **one**","user":{"login":"alice"},"created_at":"2026-10-11T18:00:00Z","updated_at":"2026-10-11T18:00:00Z","issue_url":"https://example.org/alice/project/issues/1"},` +
				`{"id":80,"body":"Second\nline","user":{"login":"bob"},"created_at":"2026-10-11T19:00:00Z","updated_at":"2026-10-11T19:30:00Z","issue_url":"https://example.org/alice/project/issues/1"},` +
				`{"id":81,"body":"Third","user":{"login":"bob"},"created_at":"2026-10-11T19:10:00Z","updated_at":"2026-10-11T19:10:00Z","issue_url":"https://example.org/alice/project/issues/1"}]`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":90,"body":"x","html_url":"https://example.org/alice/project/issues/1#issuecomment-90","issue_url":"https://example.org/alice/project/issues/1"}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Now = func() time.Time { return time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC) }
	run := func(args ...string) error {
		out.Reset()
		diagnostics.Reset()
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
	}
	require.NoError(t, run("issue", "comment", "1", "-b", "LGTM"))
	require.Equal(t, "https://example.org/alice/project/issues/1#issuecomment-90\n", out.String())
	require.NoError(t, run("issue", "comment", "add", "#1", "--body", "LGTM"))
	require.NoError(t, run("pr", "comment", "13", "-b", "Ship it"))
	require.NoError(t, run("issue", "comment", "1", "--edit-last", "-b", "Edited"))
	require.NoError(t, run("issue", "comment", "1", "--delete-last", "--yes"))
	require.NoError(t, run("issue", "comment", "1", "-b", "x", "--json", "issue_number"))
	require.JSONEq(t, `{"issue_number":1}`, out.String())
	require.Equal(t, []string{
		"POST /api/v1/repos/alice/project/issues/1/comments",
		"POST /api/v1/repos/alice/project/issues/1/comments",
		"POST /api/v1/repos/alice/project/issues/13/comments",
		"PATCH /api/v1/repos/alice/project/issues/comments/79",
		"DELETE /api/v1/repos/alice/project/issues/comments/79",
		"POST /api/v1/repos/alice/project/issues/1/comments",
	}, changed)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"issue", "comment", "craete", "1"}, `unknown command "craete" for "gt issue comment"; did you mean create?`},
		{[]string{"issue", "comment", "-b", "x"}, "missing issue number"},
		{[]string{"issue", "comment", "1", "--edit-last", "--delete-last"}, "--edit-last and --delete-last cannot be combined"},
		{[]string{"issue", "comment", "1"}, "missing required input: supply --body, --body-file or --editor"},
		{[]string{"pr", "comment", "1", "-b", "x"}, "#1 in alice/project is an issue, not a pull request; use gt issue comment 1"},
	} {
		require.ErrorContains(t, run(test.args...), test.want, test.args)
	}
	require.NoError(t, run("issue", "comment", "list", "1"))
	require.Equal(t, "id:\t79\nauthor:\talice\ncreated_at:\t2026-10-11T18:00:00Z\nurl:\t\n--\nFirst **one**\n--\n"+
		"id:\t80\nauthor:\tbob\ncreated_at:\t2026-10-11T19:00:00Z\nurl:\t\n--\nSecond\nline\n--\n"+
		"id:\t81\nauthor:\tbob\ncreated_at:\t2026-10-11T19:10:00Z\nurl:\t\n--\nThird\n--\n", out.String())
	deps.Streams.OutTTY = true
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	plainOutput(t, deps)
	require.NoError(t, run("issue", "comment", "list", "1"))
	require.Equal(t, "#79  alice • about 2 hours ago\n  First **one**\n\n"+
		"#80  bob • about 1 hour ago (edited)\n  Second\n  line\n\n"+
		"#81  bob • about 50 minutes ago\n  Third\n", out.String())
}
