// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/services"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestWebhookCreateValidatesEventsAndTypes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--events", "pushh"}, "--events must be create, delete, fork, push, issues"},
		{[]string{"--type", "slackk"}, "--type must be gitea, gogs, slack"},
		{[]string{"--type", "slack"}, "--type slack requires --config channel=#name"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			requireUsageBeforeRequests(t, tc.want, append([]string{"webhook", "create", "https://hooks.example.com/ci"}, tc.args...)...)
		})
	}
}

func TestWebhookListShowsDeliveryURLsAndSortedEvents(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/hooks", r.URL.Path)
		_, _ = w.Write([]byte(`[
			{"id":3,"type":"gitea","active":true,"events":["push","issues","issue_label"],"config":{"url":"https://ci.example.com/hook?token=private","content_type":"json"}},
			{"id":4,"type":"slack","active":false,"events":["release"],"config":{"url":"https://hooks.slack.com/services/T0/B0/private"}}]`))
	}))
	defer server.Close()
	deps, out, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	require.NoError(t, Execute(context.Background(), deps, []string{"webhook", "list"}))
	require.Equal(t, "3\tgitea\tyes\thttps://ci.example.com/hook?…\tissue_label,issues,push\n4\tslack\tno\thttps://hooks.slack.com/…\trelease\n", out.String())
	require.NotContains(t, out.String(), "private")
}

func TestWebhookTestDeliversToARepositoryWebhook(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/hooks/3":
			_, _ = w.Write([]byte(`{"id":3,"type":"gitea","active":true,"events":["push"],"config":{"url":"https://ci.example.com/hook?token=private"}}`))
		case "POST /api/v1/repos/alice/project/hooks/3/tests":
			w.WriteHeader(http.StatusNoContent)
		case "GET /api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"webhook", "test", "3", "-R", "alice/project"}))
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/hooks/3", "POST /api/v1/repos/alice/project/hooks/3/tests"}, requests)
	require.Empty(t, out.String())
	require.Equal(t, "✓ Sent a test delivery to webhook 3 (https://ci.example.com/hook?…) in alice/project\n", diagnostics.String())
	err := Execute(context.Background(), deps, []string{"webhook", "test", "4", "-R", "alice/project"})
	require.EqualError(t, err, "webhook 4 not found in alice/project")
	requireUsageBeforeRequests(t, "webhook test cannot be combined with --org; Gitea tests only repository webhooks", "webhook", "test", "3", "--org", "acme")
}

func TestWebhookEventsCollapseCompleteGroups(t *testing.T) {
	pulls := append([]string{"pull_request"}, services.WebhookEventGroups["pull_request"]...)
	for _, tc := range []struct {
		events []string
		want   string
	}{
		{[]string{"issue_assign", "issue_comment", "issue_label", "issue_milestone", "issues", "push"}, "issues (5), push"},
		{[]string{"issue_label", "issues"}, "issue_label, issues"},
		{append([]string{"create"}, pulls...), "create, pull_request (8)"},
	} {
		require.Equal(t, tc.want, collapsedEvents(tc.events))
	}
}

func TestWebhookEditAsksForTheAuthorizationHeaderOnATerminal(t *testing.T) {
	var edits []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/hooks/3", r.URL.Path)
		if r.Method == http.MethodPatch {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			edits = append(edits, body)
		}
		_, _ = w.Write([]byte(`{"id":3,"type":"gitea","active":true,"events":["push"],"config":{"url":"https://ci.example.com/hook"}}`))
	}))
	defer server.Close()
	deps, _, _ := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &scriptedPrompter{secret: "Bearer kept"}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"webhook", "edit", "3", "--active=false"}))
	require.Equal(t, []string{"Re-enter the authorization header of webhook 3 (empty if it has none)"}, prompt.secrets)
	require.Len(t, edits, 1)
	require.Equal(t, "Bearer kept", edits[0]["authorization_header"])
	require.Equal(t, false, edits[0]["active"])
}

func TestWebhookHelpWrapsLikeTheRest(t *testing.T) {
	deps, _, _ := testDeps(t)
	webhook, _, err := NewRoot(deps).Find([]string{"webhook"})
	require.NoError(t, err)
	walkCommands(webhook, func(cmd *cobra.Command) {
		for line := range strings.SplitSeq(cmd.Long, "\n") {
			require.LessOrEqual(t, len([]rune(line)), 95, "%s: %s", cmd.CommandPath(), line)
		}
	})
}
