// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDryRunRedactsTheWithheldRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("a write reached the server") }))
	defer server.Close()
	var rec DryRunRecorder
	ctx := WithSecrets(context.Background(), "hunter2")
	response, err := New(server.URL, "token", false).DryRun(&rec).Do(ctx, http.MethodPost, "repos/a/b/hooks?access_token=abc&page=2", strings.NewReader(`{"secret":"hunter2"}`), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	requests := rec.Requests()
	require.Len(t, requests, 1)
	require.Equal(t, server.URL+"/api/v1/repos/a/b/hooks?access_token=REDACTED&page=2", requests[0].URL)
	require.JSONEq(t, `{"secret":"[REDACTED]"}`, string(requests[0].Body))
}

func TestDryRunAnswersWithheldWrites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method, "a write reached the server")
		_, _ = w.Write([]byte(`{"number":5,"head":"fix"}`))
	}))
	defer server.Close()
	var rec DryRunRecorder
	client := New(server.URL, "token", false).DryRun(&rec).HTTP
	do := func(method, path string) (int, string) {
		request, err := http.NewRequest(method, server.URL+path, nil)
		require.NoError(t, err)
		response, err := client.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response.StatusCode, string(body)
	}
	status, body := do(http.MethodPatch, "/pulls/5")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{}`, body, "nothing was read from this path yet")

	status, body = do(http.MethodGet, "/pulls/5")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"number":5,"head":"fix"}`, body, "the read stays readable")

	status, body = do(http.MethodPatch, "/pulls/5")
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"number":5,"head":"fix"}`, body)

	status, body = do(http.MethodPost, "/pulls/5/merge")
	require.Equal(t, http.StatusCreated, status)
	require.JSONEq(t, `{}`, body)

	status, body = do(http.MethodDelete, "/branches/fix")
	require.Equal(t, http.StatusNoContent, status)
	require.Empty(t, body)
	require.Len(t, rec.Requests(), 4)
}

func TestDryRunAnswersEachEndpointWithItsOwnSuccess(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/api/v1/repos/a/b/issues/5/pin", http.StatusNoContent},
		{http.MethodPut, "/api/v1/repos/a/b/issues/5/lock", http.StatusNoContent},
		{http.MethodPost, "/api/v1/repos/a/b/pulls/7/merge", http.StatusOK},
		{http.MethodPost, "/api/v1/repos/a/b/issues", http.StatusCreated},
		{http.MethodDelete, "/api/v1/repos/a/b/issues/5", http.StatusNoContent},
		{http.MethodPost, "/api/v1/admin/cron/update_mirrors", http.StatusNoContent},
		{http.MethodPost, "/api/v1/repos/a/b/pulls/7/update", http.StatusOK},
		{http.MethodPost, "/git/api/v1/repos/a/b/issues/5/pin", http.StatusNoContent},
	} {
		require.Equal(t, tc.status, successStatus(tc.method, tc.path), tc.method+" "+tc.path)
	}
	for _, entry := range dryRunStatuses {
		require.NotZero(t, entry.status, entry.path)
		require.Contains(t, []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}, entry.method, entry.path)
		require.True(t, strings.HasPrefix(entry.path, "/"), entry.path)
	}
}

func TestDryRunMasksSecretsInTheReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("a write reached the server") }))
	defer server.Close()
	var rec DryRunRecorder
	client := New(server.URL, "token", false).DryRun(&rec)
	for path, body := range map[string]string{
		"repos/a/b/actions/secrets/DEPLOY": `{"data":"s3cret"}`,
		"admin/users":                      `{"username":"bob","password":"hunter2","email":"b@x"}`,
		"repos/a/b/hooks":                  `{"type":"gitea","config":{"url":"https://x","secret":"sig"}}`,
		"repos/migrate":                    `{"clone_addr":"https://x","auth_token":"tok"}`,
		"repos/a/b/actions/variables/V":    `{"value":"visible","data":"also visible"}`,
		"repos/a/b/hooks/telegram":         `{"config":{"bot_token":"bot-t0k","url":"https://x/send?access_token=url-t0k"}}`,
		"repos/x":                          `{"client_secret":"cl-s3c","token":["list-t0k"],"new_password":{"v":"nested-pw"},"api_key":12345}`,
		"repos/y":                          `{"text":"<b>&","id":9007199254740993,"authorization_header":""}`,
	} {
		response, err := client.Do(context.Background(), http.MethodPost, path, strings.NewReader(body), nil)
		require.NoError(t, err)
		defer response.Body.Close()
	}
	var all strings.Builder
	for _, request := range rec.Requests() {
		all.WriteString(string(request.Body) + "\n")
	}
	for _, secret := range []string{"s3cret", "hunter2", `"sig"`, `"tok"`, "bot-t0k", "url-t0k", "cl-s3c", "list-t0k", "nested-pw", "12345"} {
		require.NotContains(t, all.String(), secret)
	}
	for _, shown := range []string{"bob", "visible", "https://x", "<b>&", "9007199254740993", `"authorization_header":""`} {
		require.Contains(t, all.String(), shown)
	}
}
