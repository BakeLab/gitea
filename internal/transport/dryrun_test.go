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
	_, err := New(server.URL, "token", false).DryRun(&rec).Do(ctx, http.MethodPost, "repos/a/b/hooks?access_token=abc&page=2", strings.NewReader(`{"secret":"hunter2"}`), nil)
	require.NoError(t, err)
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
