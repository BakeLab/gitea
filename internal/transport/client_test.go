// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package transport

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/version"
	"github.com/stretchr/testify/require"
)

func TestInstanceScopeAndRedirect(t *testing.T) {
	leaked := false
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer outside.Close()
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/base/api/v1/user", r.URL.Path)
		require.Equal(t, "token secret", r.Header.Get("Authorization"))
		http.Redirect(w, r, outside.URL, http.StatusFound)
	}))
	defer selected.Close()
	client := New(selected.URL+"/base", "secret", false)
	_, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
	require.Error(t, err)
	require.False(t, leaked)
	for _, endpoint := range []string{outside.URL + "/api/v1/user", strings.Replace(selected.URL, "http:", "https:", 1) + "/base/api/v1/user", selected.URL + "/api/v1/user", "../user", selected.URL + "/base/api/v1/../../other", "//other.example/api/v1/user"} {
		_, err := client.URL(endpoint)
		require.Error(t, err, endpoint)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := New("https://example.org", "", false)
	_, err := client.Do(ctx, http.MethodGet, "user", nil, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestURLPreservesEscapedSegments(t *testing.T) {
	client := New("https://example.test/base%20path", "", false)
	resolved, err := client.URL("repos/alice/project/releases/tags/release%2Fv1")
	require.NoError(t, err)
	require.Equal(t, "https://example.test/base%20path/api/v1/repos/alice/project/releases/tags/release%2Fv1", resolved)
}

func TestAPIErrorIncludesOnlySanitizedServerMessage(t *testing.T) {
	const merge = "Gitea API returned HTTP %d for POST /api/v1/repos/a/b/pulls/1/merge"
	long := strings.Repeat("x", 400)
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"merge refusal", `{"message":"Please try again later","url":"https://leak.example/?key=abc"}`, fmt.Sprintf(merge, 405) + ": Please try again later", 405},
		{"token scope", `{"message":"token does not have at least one of required scope(s): [read:user]"}`, "permission denied by the Gitea instance: token does not have at least one of required scope(s): [read:user]", 403},
		{"control characters", "{\"message\":\"bad\\u001b[31m\\nnews\\u202e\"}", fmt.Sprintf(merge, 422) + ": bad [31m news", 422},
		{"capped", `{"message":"` + long + `"}`, fmt.Sprintf(merge, 500) + ": " + long[:300] + "...", 500},
		{"not json", `<html>key=abc</html>`, fmt.Sprintf(merge, 500), 500},
		{"other fields", `{"errors":["abc"]}`, fmt.Sprintf(merge, 500), 500},
		{"oversized body", `{"message":"` + strings.Repeat("y", 5000) + `"}`, fmt.Sprintf(merge, 500), 500},
		{"token echo", `{"message":"bad token secret"}`, fmt.Sprintf(merge, 400), 400},
		{"authentication", `{"message":"user alice not found"}`, "authentication rejected; run gt auth login", 401},
		{"bare not found", `{"message":"not found","url":"https://leak.example/?key=abc"}`, "not found (HTTP 404 POST /api/v1/repos/a/b/pulls/1/merge)", 404},
		{"described not found", `{"message":"label does not exist [label_id: 9]"}`, "not found (HTTP 404 POST /api/v1/repos/a/b/pulls/1/merge): label does not exist [label_id: 9]", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			_, err := New(server.URL, "secret", false).Do(context.Background(), http.MethodPost, "repos/a/b/pulls/1/merge?key=abc", strings.NewReader(`{}`), nil)
			var apiErr *APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tc.status, apiErr.Status)
			require.Equal(t, tc.want, err.Error())
			require.NotContains(t, err.Error(), "abc")
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestSudoHeaderOnAPIAndDownloadRequests(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Sudo"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	for _, sudo := range []string{"", "bob"} {
		client.Sudo = sudo
		response, err := client.Do(context.Background(), http.MethodGet, "user", nil, nil)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	response, err := client.Download(context.Background(), server.URL+"/file")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, []string{"/api/v1/user ", "/api/v1/user bob", "/file bob"}, seen)
}

func TestTransportBoundsOnlySetupAndSendsUserAgent(t *testing.T) {
	agents := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := New(server.URL, "secret", false)
	// A whole-request timeout would cut off long uploads and downloads that are still progressing.
	require.Zero(t, client.HTTP.Timeout)
	tr := client.HTTP.Transport.(userAgentTransport).base.(*http.Transport)
	require.Equal(t, 120*time.Second, tr.ResponseHeaderTimeout)
	require.Equal(t, 10*time.Second, tr.TLSHandshakeTimeout)
	for _, open := range []func() (*http.Response, error){
		func() (*http.Response, error) {
			return client.Do(context.Background(), http.MethodGet, "user", nil, nil)
		},
		func() (*http.Response, error) { return client.Download(context.Background(), server.URL+"/file") },
	} {
		response, err := open()
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	want := "gt/" + version.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	require.Equal(t, []string{want, want}, agents)
}

func TestNetworkErrorsKeepCauseWithoutURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()
	client := New(server.URL, "secret", false)
	_, err := client.Do(context.Background(), http.MethodPost, "user?token=query-secret", strings.NewReader(`{}`), nil)
	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	require.ErrorContains(t, err, "API request failed: ")
	require.NotContains(t, err.Error(), "query-secret")
	var failure *RequestFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, server.URL, failure.Server)
	_, err = client.Download(context.Background(), server.URL+"/file?signature=query-secret")
	require.ErrorAs(t, err, &opErr)
	require.ErrorContains(t, err, "download failed: ")
	require.NotContains(t, err.Error(), "query-secret")
}

func TestDebugLogsRequestsWithoutSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/a/b/labels/8":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"label does not exist [label_id: 8]"}`))
		case "/api/v1/repos/a/b/labels/9":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"label does not exist; secret-token"}`))
		default:
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	defer server.Close()
	var log bytes.Buffer
	client := New(server.URL, "secret-token", false).Debug(&log)
	_, err := client.Do(context.Background(), http.MethodGet, "repos/a/b?page=2&state=open&token=query-secret&access_token=query-secret", nil, nil)
	require.Error(t, err)
	require.Regexp(t, `^\[debug\] GET `+regexp.QuoteMeta(server.URL)+`/api/v1/repos/a/b\?access_token=REDACTED&page=2&state=open&token=REDACTED -> 418 I'm a teapot \(\d+(\.\d+)?[µm]?s\)\n$`, log.String())
	log.Reset()
	_, err = client.Do(context.Background(), http.MethodGet, "repos/a/b/labels/8", nil, nil)
	require.Regexp(t, `-> 404 Not Found \(\d+(\.\d+)?[µm]?s\): label does not exist \[label_id: 8\]\n$`, log.String())
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "label does not exist [label_id: 8]", apiErr.Message, "the logged body stays readable")
	log.Reset()
	_, err = client.Do(context.Background(), http.MethodGet, "repos/a/b/labels/9", nil, nil)
	require.Error(t, err)
	require.Regexp(t, `-> 404 Not Found \(\d+(\.\d+)?[µm]?s\)\n$`, log.String(), "a message echoing the token is dropped")
	require.NotContains(t, log.String(), "secret")
}

func TestDebugRedactsOnlySensitiveQueryValues(t *testing.T) {
	for _, tc := range []struct {
		path, key string
		redacted  bool
	}{
		{"/api/v1/repos/a/b/issues", "limit", false},
		{"/api/v1/repos/a/b/issues", "page", false},
		{"/api/v1/repos/a/b/issues", "state", false},
		{"/api/v1/repos/a/b/issues", "labels", false},
		{"/api/v1/repos/a/b/issues", "token", true},
		{"/api/v1/repos/a/b/issues", "access_token", true},
		{"/api/v1/users", "password", true},
		{"/login/oauth/authorize", "state", true},
		{"/login/oauth/access_token", "code", true},
		{"/login/oauth/access_token", "client_secret", true},
		{"/storage/blob", "X-Amz-Signature", true},
		{"/storage/blob", "X-Amz-Credential", true},
		{"/storage/blob", "X-Amz-Security-Token", true},
		{"/storage/blob", "sig", true},
		{"/storage/blob", "X-Amz-Date", false},
	} {
		require.Equal(t, tc.redacted, sensitiveQuery(tc.path, tc.key), "%s %s", tc.path, tc.key)
	}
}
