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
	"time"

	"github.com/stretchr/testify/require"
)

func TestDownloadScopeAndRedirects(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "token sensitive", r.Header.Get("Authorization"))
		if r.URL.Path == "/base/redirect" {
			http.Redirect(w, r, "/outside", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("binary bytes"))
	}))
	defer server.Close()
	client := New(server.URL+"/base", "sensitive", false)
	// Even a substituted HTTP client cannot re-enable automatic redirects.
	client.HTTP = server.Client()
	for _, raw := range []string{server.URL + "/outside", server.URL + "/base/../outside", server.URL + "/base/%2e%2e/outside", "https://example.test/base/file"} {
		_, err := client.Download(context.Background(), raw)
		require.Error(t, err)
	}
	require.Zero(t, requests)
	_, err := client.Download(context.Background(), server.URL+"/base/redirect")
	require.Error(t, err)
	require.Equal(t, 1, requests)
	response, err := client.Download(context.Background(), server.URL+"/base/file")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, "binary bytes", string(body))
}

func TestSignedDownloadsFollowOneRedirectWithoutCredentials(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Sudo"))
		_, _ = w.Write([]byte("archive"))
	}))
	defer storage.Close()
	for _, tc := range []struct {
		destination string
		status      int
		ok          bool
		requests    int
	}{
		{"/base/signed?signature=private-value", http.StatusFound, true, 1},
		{"/outside?signature=private-value", http.StatusFound, true, 1},
		{storage.URL + "/bucket/file?signature=private-value", http.StatusSeeOther, true, 0},
		{storage.URL + "/bucket/file?signature=private-value", http.StatusTemporaryRedirect, true, 0},
		{"/base/again", http.StatusFound, false, 1},
		{"ftp://storage.example.test/file?signature=private-value", http.StatusFound, false, 0},
	} {
		t.Run(tc.destination, func(t *testing.T) {
			signedRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/base/archive" {
					require.Equal(t, "token sensitive", r.Header.Get("Authorization"))
					require.Equal(t, "bob", r.Header.Get("Sudo"))
					http.Redirect(w, r, tc.destination, tc.status)
					return
				}
				signedRequests++
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Sudo"))
				if r.URL.Path == "/base/again" {
					http.Redirect(w, r, "/base/signed", http.StatusFound)
					return
				}
				_, _ = w.Write([]byte("archive"))
			}))
			defer server.Close()
			client := New(server.URL+"/base", "sensitive", false)
			client.Sudo = "bob"
			client.HTTP = server.Client() // The implementation must still suppress automatic redirects.
			response, err := client.DownloadSigned(context.Background(), server.URL+"/base/archive")
			require.Equal(t, tc.requests, signedRequests)
			if !tc.ok {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-value")
				return
			}
			require.NoError(t, err)
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, "archive", string(data))
		})
	}
}

type fakeSigner struct{ calls int }

func (s *fakeSigner) SignRequest(r *http.Request, _ []byte) error {
	s.calls++
	r.Header.Set("Signature", "signed")
	return nil
}

func TestDownloadsSignOnlyInstanceRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/base/archive" {
			require.Equal(t, "signed", r.Header.Get("Signature"))
			http.Redirect(w, r, "/base/signed?signature=private-value", http.StatusFound)
			return
		}
		require.Empty(t, r.Header.Get("Signature"))
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("archive"))
	}))
	defer server.Close()
	signer := &fakeSigner{}
	client := New(server.URL+"/base", "", false)
	client.HTTP = server.Client()
	client.Signer = signer
	response, err := client.DownloadSigned(context.Background(), server.URL+"/base/archive")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 1, signer.calls)
}

func TestURLAndDownloadShareScopeRejections(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++ }))
	defer server.Close()
	client := New(server.URL+"/base", "sensitive", false)
	client.HTTP = server.Client()
	host := strings.TrimPrefix(server.URL, "http://")
	for _, raw := range []string{
		"http://user:pass@" + host + "/base/api/v1/user", server.URL + "/base/api/v1/user#fragment", server.URL + "/base/api/v1/a\\b",
		server.URL + "/base/api/v1/./user", server.URL + "/base/api/v1/%2e%2e/user", server.URL + "/basement/api/v1/user", "//" + host + "/base/api/v1/user",
	} {
		_, err := client.URL(raw)
		require.Error(t, err, raw)
		_, err = client.Download(context.Background(), raw)
		require.Error(t, err, raw)
	}
	require.Zero(t, requests)
}

func TestSignedDownloadRejectsInvalidInstanceURL(t *testing.T) {
	_, err := New("http://[invalid", "sensitive", false).DownloadSigned(context.Background(), "http://example.test/file")
	require.ErrorContains(t, err, "invalid instance URL")
}

func TestDownloadsOutlastTheRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("slow"))
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithTimeout(20 * time.Millisecond)
	response, err := client.Download(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "slow", string(data))
}

func TestConfiguredHeadersReachOnlyTheInstance(t *testing.T) {
	var storageHeader string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageHeader = r.Header.Get("Cf-Access-Client-Secret")
		_, _ = w.Write([]byte("file"))
	}))
	defer storage.Close()
	var instanceHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceHeader = r.Header.Get("Cf-Access-Client-Secret")
		http.Redirect(w, r, storage.URL+"/signed", http.StatusFound)
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithHeaders(http.Header{"Cf-Access-Client-Secret": {"secret"}})
	response, err := client.DownloadSigned(context.Background(), server.URL+"/attachments/x")
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, "secret", instanceHeader)
	require.Empty(t, storageHeader)
}

func TestConfiguredHeadersYieldToTheRequestsOwn(t *testing.T) {
	var tenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant = r.Header.Get("X-Tenant")
	}))
	defer server.Close()
	client := New(server.URL, "", false).WithHeaders(http.Header{"X-Tenant": {"configured"}})
	response, err := client.Send(context.Background(), http.MethodGet, "version", nil, http.Header{"X-Tenant": {"explicit"}})
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, "explicit", tenant)
}
