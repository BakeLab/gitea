// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRedirectRefusesDowngradesAndLoops(t *testing.T) {
	request := func(raw string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "token secret")
		return req
	}
	from := []*http.Request{request("https://api.github.com/repos/o/r/releases/assets/1")}

	next := request("https://objects.githubusercontent.com/asset")
	require.NoError(t, checkRedirect(next, from))
	assert.Empty(t, next.Header.Get("Authorization"))

	require.EqualError(t, checkRedirect(request("http://objects.githubusercontent.com/asset"), from), "refusing redirect to http://objects.githubusercontent.com/asset")
	require.NoError(t, checkRedirect(request("http://127.0.0.1/asset"), []*http.Request{request("http://127.0.0.1/download")}), "a test server without TLS")
	require.EqualError(t, checkRedirect(next, make([]*http.Request, 5)), "too many redirects")
	assert.NotNil(t, New().HTTP.CheckRedirect)
}

func TestClientReadsReleasesCommitsAndFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/repos/o/gh-x/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v2","assets":[{"name":"gh-x-linux-amd64","digest":"sha256:ABC"}]}`))
		case "/repos/o/gh-x/releases/tags/v1":
			_, _ = w.Write([]byte(`{"tag_name":"v1"}`))
		case "/repos/o/gh-x/commits/main":
			assert.Equal(t, "application/vnd.github.sha", r.Header.Get("Accept"))
			_, _ = w.Write([]byte("abc123\n"))
		case "/repos/o/broken":
			_, _ = w.Write([]byte("<html>"))
		case "/repos/o/down", "/repos/o/down/contents/gt-extension.yml":
			w.WriteHeader(http.StatusBadGateway)
		case "/repos/o/gh-x/contents/gt-extension.yml":
			assert.Equal(t, "abc123", r.URL.Query().Get("ref"))
			_, _ = w.Write([]byte("description: X\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := Client{API: server.URL, HTTP: server.Client()}
	ctx := context.Background()

	latest, err := client.Release(ctx, "o", "gh-x", "")
	require.NoError(t, err)
	assert.Equal(t, "v2", latest.Tag)
	assert.Equal(t, "abc", latest.Assets[0].SHA256())
	assert.Empty(t, Asset{Digest: "sha512:abc"}.SHA256(), "only a SHA-256 digest can be checked")
	tagged, err := client.Release(ctx, "o", "gh-x", "v1")
	require.NoError(t, err)
	assert.Equal(t, "v1", tagged.Tag)
	_, err = client.Release(ctx, "o", "gh-x", "v0")
	require.ErrorIs(t, err, ErrNotFound)

	commit, err := client.Commit(ctx, "o", "gh-x", "main")
	require.NoError(t, err)
	assert.Equal(t, "abc123", commit)

	data, exists, err := client.File(ctx, "o", "gh-x", "abc123", "gt-extension.yml")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "description: X\n", string(data))
	_, exists, err = client.File(ctx, "o", "gh-x", "abc123", "missing.yml")
	require.NoError(t, err)
	assert.False(t, exists)

	_, err = client.Repository(ctx, "o", "missing")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = client.Repository(ctx, "o", "broken")
	require.EqualError(t, err, "GitHub returned an invalid JSON response")
	_, err = client.Repository(ctx, "o", "down")
	require.EqualError(t, err, "GitHub answered 502 Bad Gateway for repos/o/down")
	_, _, err = client.File(ctx, "o", "down", "main", "gt-extension.yml")
	require.Error(t, err, "only a missing file means it does not exist")
}

func TestDownloadFollowsRedirectsAndRefusesInsecureURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/assets/1":
			http.Redirect(w, r, "/storage/1", http.StatusFound)
		case "/storage/1":
			assert.Equal(t, "application/octet-stream", r.Header.Get("Accept"))
			_, _ = w.Write([]byte("binary"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	client := Client{API: server.URL, HTTP: &http.Client{CheckRedirect: checkRedirect}}
	ctx := context.Background()

	body, err := client.Download(ctx, Asset{Name: "gh-x-linux-amd64", URL: server.URL + "/releases/assets/1"})
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, body.Close())
	require.NoError(t, err)
	assert.Equal(t, "binary", string(data))

	_, err = client.Download(ctx, Asset{Name: "gh-x-linux-amd64", URL: server.URL + "/expired"})
	require.EqualError(t, err, "GitHub answered 403 Forbidden for gh-x-linux-amd64")
	for _, raw := range []string{"http://example.com/gh-x", "::"} {
		_, err = client.Download(ctx, Asset{Name: "gh-x", URL: raw})
		require.EqualError(t, err, "refusing to download gh-x over an insecure connection", raw)
	}
}
