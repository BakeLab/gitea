// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookEditPreservesSettingsAndRedactsCredentials(t *testing.T) {
	mutations, reads := 0, 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/owner/repo/hooks/1", r.URL.Path)
		if r.Method == http.MethodGet {
			reads++
			if mutations > 0 {
				_, _ = w.Write([]byte(`{"id":1,"type":"slack","active":false,"events":["issues","release"],"authorization_header":"private-header","config":{"secret":"private-secret","url":"https://hook.invalid/private-path"},"updated_at":"2026-10-02T12:00:00Z"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"type":"slack","active":true,"events":["issues","release"],"branch_filter":"main","authorization_header":"private-header","config":{"url":"https://hook.invalid/private-path","channel":"old","username":"robot","secret":"private-secret"}}`))
			return
		}
		assert.Equal(t, http.MethodPatch, r.Method)
		mutations++
		var payload map[string]any
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, []any{"issues_only", "release"}, payload["events"])
		assert.Equal(t, "main", payload["branch_filter"])
		assert.Equal(t, "", payload["authorization_header"])
		assert.Equal(t, false, payload["active"])
		assert.Equal(t, map[string]any{"url": "https://hook.invalid/private-path", "channel": "new", "username": "robot", "secret": "private-secret"}, payload["config"])
		w.WriteHeader(http.StatusOK)
	}))
	service := Webhooks{Client: client, Scope: WebhookScope{Repository: "owner/repo"}}
	active := false
	authorization := ""
	item, err := service.Edit(context.Background(), 1, WebhookOptions{Active: &active, Authorization: &authorization, Config: map[string]string{"channel": "new"}})
	require.NoError(t, err)
	data, err := json.Marshal(item)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private")
	require.NotContains(t, string(data), "config")
	require.False(t, item.Active)
	require.Equal(t, "2026-10-02T12:00:00Z", item.UpdatedAt)
	require.Equal(t, 1, mutations)
	require.Equal(t, 2, reads)
	_, err = service.Edit(context.Background(), 1, WebhookOptions{Active: &active})
	require.ErrorContains(t, err, "authorization choice")
	_, err = service.Edit(context.Background(), 1, WebhookOptions{Authorization: &authorization, Config: map[string]string{"secret": "new"}})
	require.ErrorContains(t, err, "signing secret")
	require.Equal(t, 1, mutations)
}

func TestWebhookCreateMarksOnlyGlobalHooksAsSystem(t *testing.T) {
	for name, test := range map[string]struct {
		scope      WebhookScope
		wantPath   string
		wantConfig map[string]any
	}{
		"global":       {scope: WebhookScope{Global: true}, wantPath: "/api/v1/admin/hooks", wantConfig: map[string]any{"url": "https://hook.invalid", "is_system_webhook": "true"}},
		"organization": {scope: WebhookScope{Organization: "team"}, wantPath: "/api/v1/orgs/team/hooks", wantConfig: map[string]any{"url": "https://hook.invalid"}},
		"repository":   {scope: WebhookScope{Repository: "owner/repo"}, wantPath: "/api/v1/repos/owner/repo/hooks", wantConfig: map[string]any{"url": "https://hook.invalid"}},
	} {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, test.wantPath, r.URL.Path)
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				_, _ = w.Write([]byte(`{"id":4,"type":"gitea","active":true,"config":{"url":"https://hook.invalid"}}`))
			}))
			config := map[string]string{"url": "https://hook.invalid"}
			item, err := (Webhooks{Client: client, Scope: test.scope}).Create(context.Background(), WebhookOptions{Type: "gitea", Config: config})
			require.NoError(t, err)
			require.EqualValues(t, 4, item.ID)
			require.Equal(t, []string{}, item.Events)
			require.Equal(t, test.wantConfig, payload["config"])
			require.Equal(t, map[string]string{"url": "https://hook.invalid"}, config, "caller's config must not change")
		})
	}
}

func TestWebhookGlobalEditUsesReturnedHook(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		var payload map[string]any
		if r.Method == http.MethodPatch {
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			assert.Equal(t, []any{"push"}, payload["events"])
			assert.Nil(t, payload["config"])
			assert.Nil(t, payload["active"])
		}
		_, _ = w.Write([]byte(`{"id":2,"type":"gitea","active":true,"events":["push"]}`))
	}))
	authorization := "Bearer value"
	item, err := (Webhooks{Client: client, Scope: WebhookScope{Global: true}}).Edit(context.Background(), 2, WebhookOptions{Authorization: &authorization})
	require.NoError(t, err)
	require.Equal(t, Webhook{ID: 2, Type: "gitea", Active: true, Events: []string{"push"}}, item)
	require.Equal(t, []string{"GET /api/v1/admin/hooks/2", "PATCH /api/v1/admin/hooks/2"}, requests)
}

func TestWebhookScopesSelectEndpoints(t *testing.T) {
	for name, tc := range map[string]struct {
		scope WebhookScope
		path  string
	}{
		"global":       {scope: WebhookScope{Global: true}, path: "/api/v1/admin/hooks"},
		"account":      {scope: WebhookScope{Account: true}, path: "/api/v1/user/hooks"},
		"organization": {scope: WebhookScope{Organization: "team"}, path: "/api/v1/orgs/team/hooks"},
		"repository":   {scope: WebhookScope{Repository: "owner/repo"}, path: "/api/v1/repos/owner/repo/hooks"},
	} {
		t.Run(name, func(t *testing.T) {
			var requests []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.RequestURI())
				if r.Method == http.MethodDelete {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = w.Write([]byte(`[{"id":3,"type":"gitea","events":null}]`))
			}))
			service := Webhooks{Client: client, Scope: tc.scope}
			items, err := service.List(context.Background(), 10, 2)
			require.NoError(t, err)
			require.Equal(t, []Webhook{{ID: 3, Type: "gitea", Events: []string{}}}, items.Items)
			require.NoError(t, service.Delete(context.Background(), 3))
			require.Equal(t, []string{"GET " + tc.path + "?limit=10&page=2", "DELETE " + tc.path + "/3"}, requests)
		})
	}
}

func TestWebhookFromMapsTimestamps(t *testing.T) {
	hook := decode[gitea.Hook](t, `{"id":1,"type":"gitea","active":true,"events":["push"],"branch_filter":"main",
		"created_at":"2026-10-01T12:00:00Z","updated_at":"2026-10-02T12:00:00+02:00","config":{"url":"https://hooks.invalid/ci","content_type":"form"}}`)
	require.Equal(t, Webhook{
		ID: 1, Type: "gitea", URL: "https://hooks.invalid/ci", ContentType: "form", Active: true, Events: []string{"push"}, BranchFilter: "main",
		CreatedAt: "2026-10-01T12:00:00Z", UpdatedAt: "2026-10-02T12:00:00+02:00",
	}, webhookFrom(hook, false))
}

func TestWebhookFromSortsEvents(t *testing.T) {
	hook := decode[gitea.Hook](t, `{"id":1,"type":"gitea","events":["push","issues","issue_label","create"]}`)
	require.Equal(t, []string{"create", "issue_label", "issues", "push"}, webhookFrom(hook, false).Events)
}

func TestWebhookFromRedactsDeliveryURLs(t *testing.T) {
	for _, tc := range []struct{ adapter, raw, want string }{
		{"gitea", "https://ci.example.com/hooks/gitea", "https://ci.example.com/hooks/gitea"},
		{"gitea", "https://user:pass@ci.example.com/hook?token=private", "https://ci.example.com/hook?…"},
		{"packagist", "https://packagist.org/api/update-package?username=a&apiToken=private", "https://packagist.org/api/update-package?…"},
		{"slack", "https://hooks.slack.com/services/T0/B0/private", "https://hooks.slack.com/…"},
		{"discord", "https://discord.com/api/webhooks/1/private", "https://discord.com/…"},
		{"telegram", "https://api.telegram.org/botprivate/sendMessage?chat_id=1", "https://api.telegram.org/…?…"},
		{"matrix", "https://matrix.example.com/_matrix/client/v3/rooms/!room/send/m.room.message", "https://matrix.example.com/_matrix/client/v3/rooms/!room/send/m.room.message"},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			hook := &gitea.Hook{ID: 1, Type: tc.adapter, Config: map[string]string{"url": tc.raw}}
			require.Equal(t, tc.want, webhookFrom(hook, false).URL)
			require.Equal(t, tc.raw, webhookFrom(hook, true).URL)
		})
	}
}

func TestWebhookEditKeepsEventGroupsNarrow(t *testing.T) {
	issues := []string{"issue_assign", "issue_comment", "issue_label", "issue_milestone", "issues"}
	pulls := []string{"pull_request", "pull_request_assign", "pull_request_comment", "pull_request_label", "pull_request_milestone", "pull_request_review", "pull_request_review_request", "pull_request_sync"}
	require.Equal(t, []string{"push", "issues_only", "pull_request_only", "pull_request_review"}, narrowedEvents([]string{"push", "issues", "pull_request", "pull_request_review"}))
	require.Equal(t, []string{"issues_only", "issue_label"}, narrowedEvents([]string{"issues", "issue_label"}))
	require.Equal(t, issues, narrowedEvents(issues))
	require.Equal(t, pulls, narrowedEvents(pulls))
}

func TestWebhookNotFoundNamesTheSelectedScope(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	for name, tc := range map[string]struct {
		scope WebhookScope
		want  string
	}{
		"organization": {WebhookScope{Repository: "owner/repo", Organization: "acme"}, "webhook 9 not found in acme"},
		"account":      {WebhookScope{Repository: "owner/repo", Account: true}, "webhook 9 not found"},
		"global":       {WebhookScope{Repository: "owner/repo", Global: true}, "webhook 9 not found"},
		"repository":   {WebhookScope{Repository: "owner/repo"}, "webhook 9 not found in owner/repo"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (Webhooks{Client: client, Scope: tc.scope}).View(context.Background(), 9)
			require.EqualError(t, err, tc.want)
		})
	}
}
